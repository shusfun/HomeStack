package desktop

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/wangshangbin/homestack/internal/control"
	"github.com/wangshangbin/homestack/internal/protocol"
	"github.com/wangshangbin/homestack/internal/secure"
	"github.com/wangshangbin/homestack/internal/securestore"
	"github.com/wangshangbin/homestack/internal/tailscale"
	"golang.org/x/oauth2"
)

type OpenURL func(string) error

type Joiner struct {
	HTTPClient *http.Client
	OpenURL    OpenURL
}

type JoinResult struct {
	DeviceID  string `json:"device_id"`
	AgentURL  string `json:"agent_url"`
	TailnetIP string `json:"tailnet_ip,omitempty"`
	Message   string `json:"message"`
}

type metaResponse struct {
	OIDC             control.OIDCMetadata `json:"oidc"`
	SigningKeyID     string               `json:"signing_key_id"`
	SigningPublicKey string               `json:"signing_public_key"`
}

func (j *Joiner) Join(ctx context.Context, rawDescriptor string) (JoinResult, error) {
	descriptor, err := protocol.ParseJoinDescriptor(strings.TrimSpace(rawDescriptor))
	if err != nil {
		return JoinResult{}, err
	}
	if j.OpenURL == nil {
		return JoinResult{}, errors.New("桌面端未配置系统浏览器打开能力")
	}
	client := j.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	meta, err := fetchMetadata(ctx, client, descriptor.Server)
	if err != nil {
		return JoinResult{}, err
	}
	provider, err := oidc.NewProvider(ctx, meta.OIDC.Issuer)
	if err != nil {
		return JoinResult{}, fmt.Errorf("加载 Pocket ID OIDC 配置失败: %w", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return JoinResult{}, fmt.Errorf("启动 OIDC 本机回调失败: %w", err)
	}
	defer listener.Close()
	redirectURL := "http://" + listener.Addr().String() + "/callback"
	state, err := randomURLToken(32)
	if err != nil {
		return JoinResult{}, err
	}
	nonce, err := randomURLToken(32)
	if err != nil {
		return JoinResult{}, err
	}
	verifier, err := randomURLToken(64)
	if err != nil {
		return JoinResult{}, err
	}
	challengeBytes := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(challengeBytes[:])
	oauthConfig := oauth2.Config{
		ClientID: meta.OIDC.ClientID, Endpoint: provider.Endpoint(), RedirectURL: redirectURL,
		Scopes: []string{oidc.ScopeOpenID, "profile", "email", "groups"},
	}
	authURL := oauthConfig.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.SetAuthURLParam("code_challenge", challenge), oauth2.SetAuthURLParam("code_challenge_method", "S256"))
	codeChannel := make(chan string, 1)
	errorChannel := make(chan error, 1)
	callbackServer := &http.Server{ReadHeaderTimeout: 5 * time.Second}
	callbackServer.Handler = callbackHandler(state, codeChannel, errorChannel)
	go func() {
		if serveErr := callbackServer.Serve(listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			select {
			case errorChannel <- serveErr:
			default:
			}
		}
	}()
	if err := j.OpenURL(authURL); err != nil {
		return JoinResult{}, fmt.Errorf("打开 Pocket ID 登录页失败: %w", err)
	}
	loginContext, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	var code string
	select {
	case code = <-codeChannel:
	case callbackErr := <-errorChannel:
		return JoinResult{}, callbackErr
	case <-loginContext.Done():
		return JoinResult{}, fmt.Errorf("等待 Pocket ID 登录失败: %w", loginContext.Err())
	}
	shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), time.Second)
	_ = callbackServer.Shutdown(shutdownContext)
	shutdownCancel()
	token, err := oauthConfig.Exchange(ctx, code, oauth2.SetAuthURLParam("code_verifier", verifier))
	if err != nil {
		return JoinResult{}, fmt.Errorf("兑换 OIDC 授权码失败: %w", err)
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		return JoinResult{}, errors.New("Pocket ID 未返回 ID Token")
	}
	idToken, err := provider.Verifier(&oidc.Config{ClientID: meta.OIDC.ClientID}).Verify(ctx, rawIDToken)
	if err != nil {
		return JoinResult{}, fmt.Errorf("验证 Pocket ID Token 失败: %w", err)
	}
	var tokenClaims struct {
		Nonce string `json:"nonce"`
	}
	if err := idToken.Claims(&tokenClaims); err != nil || tokenClaims.Nonce != nonce {
		return JoinResult{}, errors.New("OIDC nonce 验证失败")
	}
	privateKey, err := securestore.LoadOrCreateDeviceKey()
	if err != nil {
		return JoinResult{}, err
	}
	joinResponse, err := exchangeJoin(ctx, client, descriptor, rawIDToken, base64.RawURLEncoding.EncodeToString(privateKey.PublicKey().Bytes()))
	if err != nil {
		return JoinResult{}, err
	}
	var credential protocol.DeviceCredentialV1
	if err := secure.OpenJSON(privateKey, joinResponse.SealedCredential, &credential); err != nil {
		return JoinResult{}, err
	}
	if credential.DeviceID != joinResponse.DeviceID || !time.Now().UTC().Before(credential.ExpiresAt) {
		return JoinResult{}, errors.New("Control 返回的设备凭据无效或已过期")
	}
	tailscaleClient, err := tailscale.New()
	if err != nil {
		return JoinResult{}, err
	}
	if err := tailscaleClient.VerifyVersion(ctx); err != nil {
		return JoinResult{}, err
	}
	if err := tailscaleClient.Up(ctx, credential.HeadscaleLoginServer, credential.HeadscaleAuthKey); err != nil {
		return JoinResult{}, err
	}
	if err := tailscaleClient.VerifyNetworkPolicy(ctx); err != nil {
		return JoinResult{}, err
	}
	credential.HeadscaleAuthKey = ""
	controlPublicKey, err := base64.RawURLEncoding.DecodeString(meta.SigningPublicKey)
	if err != nil {
		return JoinResult{}, errors.New("Control 签名公钥编码无效")
	}
	var signedConfig protocol.SignedDeviceConfigV1
	if err := secure.VerifyJWS(joinResponse.SignedConfig, ed25519.PublicKey(controlPublicKey), meta.SigningKeyID, &signedConfig); err != nil {
		return JoinResult{}, fmt.Errorf("Control 签名配置验证失败: %w", err)
	}
	if signedConfig.DeviceID != joinResponse.DeviceID || signedConfig.DeviceName != joinResponse.DeviceName {
		return JoinResult{}, errors.New("Control 签名配置与加入结果不一致")
	}
	if err := securestore.SaveDeviceProfile(securestore.DeviceProfile{
		DeviceID: joinResponse.DeviceID, DeviceName: joinResponse.DeviceName, ControlKeyID: meta.SigningKeyID,
		ControlPublicKey: meta.SigningPublicKey, SignedConfig: joinResponse.SignedConfig, Credential: credential,
	}); err != nil {
		return JoinResult{}, err
	}
	status, err := tailscaleClient.Status(ctx)
	if err != nil {
		return JoinResult{}, err
	}
	return JoinResult{DeviceID: joinResponse.DeviceID, AgentURL: signedConfig.AgentURL, TailnetIP: status.TailnetIP, Message: "设备已安全加入 HomeStack"}, nil
}

func fetchMetadata(ctx context.Context, client *http.Client, server string) (metaResponse, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server+"/api/v1/meta", nil)
	if err != nil {
		return metaResponse{}, err
	}
	response, err := client.Do(request)
	if err != nil {
		return metaResponse{}, fmt.Errorf("读取 Control 元数据失败: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return metaResponse{}, responseError("读取 Control 元数据", response)
	}
	var meta metaResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&meta); err != nil {
		return metaResponse{}, fmt.Errorf("解析 Control 元数据失败: %w", err)
	}
	return meta, nil
}

func exchangeJoin(ctx context.Context, client *http.Client, descriptor protocol.JoinDescriptorV1, idToken, publicKey string) (protocol.JoinResponseV1, error) {
	body, err := json.Marshal(protocol.JoinRequestV1{Version: protocol.JoinVersion, Code: descriptor.Code, EncryptionPublicKey: publicKey})
	if err != nil {
		return protocol.JoinResponseV1{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, descriptor.Server+"/api/v1/join/exchange", strings.NewReader(string(body)))
	if err != nil {
		return protocol.JoinResponseV1{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+idToken)
	response, err := client.Do(request)
	if err != nil {
		return protocol.JoinResponseV1{}, fmt.Errorf("兑换 HomeStack 邀请失败: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		return protocol.JoinResponseV1{}, responseError("兑换 HomeStack 邀请", response)
	}
	var result protocol.JoinResponseV1
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&result); err != nil {
		return protocol.JoinResponseV1{}, fmt.Errorf("解析 HomeStack 加入结果失败: %w", err)
	}
	return result, nil
}

func callbackHandler(expectedState string, codes chan<- string, failures chan<- error) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/callback" || request.URL.Query().Get("state") != expectedState {
			http.Error(writer, "OIDC 回调无效", http.StatusBadRequest)
			return
		}
		if message := request.URL.Query().Get("error"); message != "" {
			failures <- fmt.Errorf("Pocket ID 拒绝登录: %s", html.EscapeString(message))
			http.Error(writer, "登录未完成，可以关闭此窗口。", http.StatusUnauthorized)
			return
		}
		code := request.URL.Query().Get("code")
		if code == "" {
			http.Error(writer, "OIDC 回调缺少授权码", http.StatusBadRequest)
			return
		}
		codes <- code
		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		writer.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
		_, _ = io.WriteString(writer, "<!doctype html><html lang=\"zh-CN\"><body><p>登录完成，可以关闭此窗口并返回 HomeStack。</p></body></html>")
	})
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

func randomURLToken(size int) (string, error) {
	return randomCryptoToken(size)
}
