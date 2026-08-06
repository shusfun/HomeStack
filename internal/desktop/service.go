package desktop

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wangshangbin/homestack/internal/protocol"
	"github.com/wangshangbin/homestack/internal/securestore"
	"github.com/wangshangbin/homestack/internal/tailscale"
)

type Service struct {
	client  *APIClient
	updates *UpdateService
}

type SessionStatus struct {
	LoggedIn   bool      `json:"logged_in"`
	ControlURL string    `json:"control_url,omitempty"`
	ExpiresAt  time.Time `json:"expires_at,omitempty"`
}

type LocalStatus struct {
	Online     bool   `json:"online"`
	TailnetIP  string `json:"tailnet_ip,omitempty"`
	Connection string `json:"connection"`
	Error      string `json:"error,omitempty"`
}

type EnrollmentResult struct {
	Command   string    `json:"command"`
	ExpiresAt time.Time `json:"expires_at"`
}

func NewService() *Service {
	return &Service{client: &APIClient{OpenURL: openSystemURL}, updates: newUpdateService()}
}

func (s *Service) ConfigureUpdater(app *application.App) error {
	if err := s.updates.Configure(app); err != nil {
		s.updates.setState("error", err.Error())
		return err
	}
	return nil
}

func (s *Service) UpdateStatus() UpdateStatus { return s.updates.Status() }

func (s *Service) CheckForUpdates() (UpdateStatus, error) { return s.updates.Check() }

func (s *Service) DownloadUpdate() (UpdateStatus, error) { return s.updates.DownloadAndInstall() }

func (s *Service) RestartForUpdate() error { return s.updates.Restart() }

func (s *Service) SkipUpdate(version string) error { return s.updates.SkipVersion(version) }

func (s *Service) OpenUpdateRelease() error { return s.updates.OpenReleasePage() }

func openSystemURL(target string) error {
	app := application.Get()
	if app == nil || app.Browser == nil {
		return errors.New("Wails 浏览器服务尚未初始化")
	}
	return app.Browser.OpenURL(target)
}

func (s *Service) Providers(controlURL string) ([]Provider, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return s.client.Providers(ctx, controlURL)
}

func (s *Service) Login(controlURL, provider string) (SessionStatus, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	session, err := s.client.Login(ctx, controlURL, provider)
	if err != nil {
		return SessionStatus{}, err
	}
	return SessionStatus{LoggedIn: true, ControlURL: session.ControlURL, ExpiresAt: session.AccessExpiresAt}, nil
}

func (s *Service) Session() (SessionStatus, error) {
	hasSession, err := securestore.HasAppSession()
	if err != nil {
		return SessionStatus{}, err
	}
	if !hasSession {
		return SessionStatus{}, nil
	}
	session, err := securestore.LoadAppSession()
	if err != nil {
		return SessionStatus{}, err
	}
	return SessionStatus{LoggedIn: true, ControlURL: session.ControlURL, ExpiresAt: session.AccessExpiresAt}, nil
}

func (s *Service) Logout() error {
	return securestore.DeleteAppSession()
}

func (s *Service) Devices() ([]Device, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var response struct {
		Devices []Device `json:"devices"`
	}
	if err := s.client.AuthenticatedJSON(ctx, http.MethodGet, "/api/v1/devices", nil, &response, http.StatusOK); err != nil {
		return nil, err
	}
	return response.Devices, nil
}

func (s *Service) ConnectTailnet() (LocalStatus, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var key struct {
		LoginServer string `json:"login_server"`
		AuthKey     string `json:"auth_key"`
	}
	if err := s.client.AuthenticatedJSON(ctx, http.MethodPost, "/api/v1/tailnet/auth-keys", map[string]any{}, &key, http.StatusCreated); err != nil {
		return LocalStatus{}, err
	}
	client, err := tailscale.New()
	if err != nil {
		return LocalStatus{}, err
	}
	if err := client.VerifyVersion(ctx); err != nil {
		return LocalStatus{}, err
	}
	if err := client.Up(ctx, key.LoginServer, key.AuthKey); err != nil {
		return LocalStatus{}, err
	}
	if err := client.VerifyNetworkPolicy(ctx); err != nil {
		return LocalStatus{}, err
	}
	return localTailnetStatus(ctx, client)
}

func (s *Service) CreateEnrollment(policy protocol.JoinPolicyV1) (EnrollmentResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var response struct {
		JoinInfo  string    `json:"join_info"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := s.client.AuthenticatedJSON(ctx, http.MethodPost, "/api/v1/device-enrollments", policy, &response, http.StatusCreated); err != nil {
		return EnrollmentResult{}, err
	}
	return EnrollmentResult{
		Command: fmt.Sprintf(
			"homestack-agent enroll --descriptor '%s' --name '%s' --agent-url '%s'",
			response.JoinInfo, shellSingleQuote(policy.DeviceName), shellSingleQuote(policy.AgentURL),
		),
		ExpiresAt: response.ExpiresAt,
	}, nil
}

func shellSingleQuote(value string) string {
	return strings.ReplaceAll(value, "'", "'\"'\"'")
}

func (s *Service) OpenDevice(deviceID string) error {
	if deviceID == "" {
		return errors.New("设备 ID 不能为空")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var response struct {
		URL string `json:"url"`
	}
	path := "/api/v1/devices/" + url.PathEscape(deviceID) + "/tickets"
	if err := s.client.AuthenticatedJSON(ctx, http.MethodPost, path, map[string]any{}, &response, http.StatusCreated); err != nil {
		return err
	}
	return openSystemURL(response.URL)
}

func (s *Service) LocalStatus() LocalStatus {
	client, err := tailscale.New()
	if err != nil {
		return LocalStatus{Connection: "未连接", Error: err.Error()}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := client.VerifyVersion(ctx); err != nil {
		return LocalStatus{Connection: "未连接", Error: err.Error()}
	}
	status, err := localTailnetStatus(ctx, client)
	if err != nil {
		return LocalStatus{Connection: "未连接", Error: err.Error()}
	}
	return status
}

func localTailnetStatus(ctx context.Context, client *tailscale.Client) (LocalStatus, error) {
	status, err := client.Status(ctx)
	if err != nil {
		return LocalStatus{}, err
	}
	return LocalStatus{Online: status.Online, TailnetIP: status.TailnetIP, Connection: status.Connection}, nil
}
