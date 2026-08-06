package desktop

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/wangshangbin/homestack/internal/protocol"
	"github.com/wangshangbin/homestack/internal/securestore"
)

type OpenURL func(string) error

type APIClient struct {
	HTTPClient *http.Client
	OpenURL    OpenURL
}

type Provider struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type Device struct {
	ID       string                  `json:"id"`
	Name     string                  `json:"name"`
	AgentURL string                  `json:"agent_url"`
	Status   protocol.DeviceStatusV1 `json:"status"`
}

type LoginResult struct {
	ControlURL string     `json:"control_url"`
	Providers  []Provider `json:"providers"`
	LoggedIn   bool       `json:"logged_in"`
}

type tokenResponse struct {
	AccessToken      string    `json:"access_token"`
	ExpiresAt        time.Time `json:"expires_at"`
	RefreshToken     string    `json:"refresh_token"`
	RefreshExpiresAt time.Time `json:"refresh_expires_at"`
}

func (c *APIClient) Providers(ctx context.Context, controlURL string) ([]Provider, error) {
	controlURL, err := validateControlURL(controlURL)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Providers []Provider `json:"providers"`
	}
	if err := c.requestJSON(ctx, http.MethodGet, controlURL+"/api/v1/meta", "", nil, &payload, http.StatusOK); err != nil {
		return nil, err
	}
	if len(payload.Providers) == 0 {
		return nil, errors.New("Control 未提供登录方式")
	}
	return payload.Providers, nil
}

func (c *APIClient) Login(ctx context.Context, controlURL, provider string) (securestore.AppSession, error) {
	controlURL, err := validateControlURL(controlURL)
	if err != nil {
		return securestore.AppSession{}, err
	}
	if provider == "" {
		return securestore.AppSession{}, errors.New("必须选择登录方式")
	}
	if c.OpenURL == nil {
		return securestore.AppSession{}, errors.New("桌面端未配置系统浏览器打开能力")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return securestore.AppSession{}, fmt.Errorf("启动本机登录回调失败: %w", err)
	}
	defer listener.Close()
	redirectURI := "http://" + listener.Addr().String() + "/callback"
	state, err := randomCryptoToken(32)
	if err != nil {
		return securestore.AppSession{}, err
	}
	verifier, err := randomCryptoToken(64)
	if err != nil {
		return securestore.AppSession{}, err
	}
	digest := sha256.Sum256([]byte(verifier))
	query := url.Values{
		"redirect_uri":   {redirectURI},
		"state":          {state},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(digest[:])},
	}
	authURL := controlURL + "/auth/app/start/" + url.PathEscape(provider) + "?" + query.Encode()
	codes := make(chan string, 1)
	failures := make(chan error, 1)
	callbackServer := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: callbackHandler(state, codes, failures)}
	go func() {
		if serveErr := callbackServer.Serve(listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			select {
			case failures <- serveErr:
			default:
			}
		}
	}()
	if err := c.OpenURL(authURL); err != nil {
		return securestore.AppSession{}, fmt.Errorf("打开登录页失败: %w", err)
	}
	var code string
	select {
	case code = <-codes:
	case callbackErr := <-failures:
		return securestore.AppSession{}, callbackErr
	case <-ctx.Done():
		return securestore.AppSession{}, fmt.Errorf("等待登录失败: %w", ctx.Err())
	}
	shutdownContext, cancel := context.WithTimeout(context.Background(), time.Second)
	_ = callbackServer.Shutdown(shutdownContext)
	cancel()
	var tokens tokenResponse
	if err := c.requestJSON(ctx, http.MethodPost, controlURL+"/api/v1/auth/app/token", "", map[string]string{
		"code": code, "code_verifier": verifier,
	}, &tokens, http.StatusOK); err != nil {
		return securestore.AppSession{}, err
	}
	session := securestore.AppSession{
		ControlURL: controlURL, AccessToken: tokens.AccessToken, AccessExpiresAt: tokens.ExpiresAt,
		RefreshToken: tokens.RefreshToken, RefreshExpiresAt: tokens.RefreshExpiresAt,
	}
	if err := securestore.SaveAppSession(session); err != nil {
		return securestore.AppSession{}, err
	}
	return session, nil
}

func (c *APIClient) AuthenticatedJSON(ctx context.Context, method, path string, body, target any, expected int) error {
	session, err := securestore.LoadAppSession()
	if err != nil {
		return err
	}
	if !time.Now().UTC().Before(session.AccessExpiresAt) {
		session, err = c.refresh(ctx, session)
		if err != nil {
			return err
		}
	}
	return c.requestJSON(ctx, method, session.ControlURL+path, session.AccessToken, body, target, expected)
}

func (c *APIClient) refresh(ctx context.Context, session securestore.AppSession) (securestore.AppSession, error) {
	if !time.Now().UTC().Before(session.RefreshExpiresAt) {
		return securestore.AppSession{}, errors.New("App 登录已过期，请重新登录")
	}
	var tokens tokenResponse
	if err := c.requestJSON(ctx, http.MethodPost, session.ControlURL+"/api/v1/auth/app/refresh", "", map[string]string{
		"refresh_token": session.RefreshToken,
	}, &tokens, http.StatusOK); err != nil {
		return securestore.AppSession{}, fmt.Errorf("刷新 App 登录失败: %w", err)
	}
	updated := securestore.AppSession{
		ControlURL: session.ControlURL, AccessToken: tokens.AccessToken, AccessExpiresAt: tokens.ExpiresAt,
		RefreshToken: tokens.RefreshToken, RefreshExpiresAt: tokens.RefreshExpiresAt,
	}
	if err := securestore.SaveAppSession(updated); err != nil {
		return securestore.AppSession{}, err
	}
	return updated, nil
}

func (c *APIClient) requestJSON(ctx context.Context, method, endpoint, accessToken string, body, target any, expected int) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = strings.NewReader(string(data))
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if accessToken != "" {
		request.Header.Set("Authorization", "Bearer "+accessToken)
	}
	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("请求 Control 失败: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != expected {
		return responseError("Control 请求", response)
	}
	if target == nil || response.StatusCode == http.StatusNoContent {
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(target); err != nil {
		return fmt.Errorf("解析 Control 响应失败: %w", err)
	}
	return nil
}

func callbackHandler(expectedState string, codes chan<- string, failures chan<- error) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/callback" || request.URL.Query().Get("state") != expectedState {
			http.Error(writer, "登录回调无效", http.StatusBadRequest)
			return
		}
		if message := request.URL.Query().Get("error"); message != "" {
			failures <- fmt.Errorf("登录被拒绝: %s", html.EscapeString(message))
			http.Error(writer, "登录未完成，可以关闭此窗口。", http.StatusUnauthorized)
			return
		}
		code := request.URL.Query().Get("code")
		if code == "" {
			http.Error(writer, "登录回调缺少授权码", http.StatusBadRequest)
			return
		}
		codes <- code
		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		writer.Header().Set("Content-Security-Policy", "default-src 'none'")
		_, _ = io.WriteString(writer, "<!doctype html><html lang=\"zh-CN\"><body><p>登录完成，可以关闭此窗口并返回 HomeStack。</p></body></html>")
	})
}

func validateControlURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return "", errors.New("Control 地址必须是无凭据、无路径的有效 HTTPS 地址")
	}
	parsed.Path = ""
	return strings.TrimRight(parsed.String(), "/"), nil
}

func responseError(action string, response *http.Response) error {
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("%s失败，HTTP %d，且读取错误响应失败: %w", action, response.StatusCode, err)
	}
	var payload protocol.ErrorResponse
	if json.Unmarshal(data, &payload) == nil && payload.Error.Message != "" {
		return fmt.Errorf("%s失败，HTTP %d: %s", action, response.StatusCode, payload.Error.Message)
	}
	return fmt.Errorf("%s失败，HTTP %d: %s", action, response.StatusCode, strings.TrimSpace(string(data)))
}
