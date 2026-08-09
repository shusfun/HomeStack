package desktop

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wangshangbin/homestack/internal/buildinfo"
	"github.com/wangshangbin/homestack/internal/managed"
	"github.com/wangshangbin/homestack/internal/protocol"
	"github.com/wangshangbin/homestack/internal/secure"
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
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	session, err := s.client.Login(ctx, controlURL, provider)
	if err != nil {
		return SessionStatus{}, err
	}
	tailnet, err := tailscale.New()
	if err != nil {
		_ = securestore.DeleteAppSession()
		return SessionStatus{}, err
	}
	status, err := tailnet.Status(ctx)
	if err != nil {
		_ = securestore.DeleteAppSession()
		return SessionStatus{}, err
	}
	content, err := prepareManagedContent(ctx, nil)
	if err != nil {
		_ = securestore.DeleteAppSession()
		return SessionStatus{}, err
	}
	if _, err := s.client.RegisterCurrentNode(ctx, status, &content); err != nil {
		_ = securestore.DeleteAppSession()
		return SessionStatus{}, err
	}
	if err := ConfigureNodeAutostart(); err != nil {
		_ = securestore.DeleteAppSession()
		return SessionStatus{}, err
	}
	if err := RestartNode(); err != nil {
		_ = securestore.DeleteAppSession()
		return SessionStatus{}, err
	}
	return SessionStatus{LoggedIn: true, ControlURL: session.ControlURL, ExpiresAt: session.AccessExpiresAt}, nil
}

func (s *Service) Activate(controlURL, code string) (SessionStatus, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	tailnet, err := tailscale.New()
	if err != nil {
		return SessionStatus{}, err
	}
	status, err := tailnet.Status(ctx)
	if err != nil {
		return SessionStatus{}, err
	}
	content, err := prepareManagedContent(ctx, nil)
	if err != nil {
		return SessionStatus{}, err
	}
	if _, err := s.client.ActivateCurrentNode(ctx, controlURL, code, status, &content); err != nil {
		return SessionStatus{}, err
	}
	if err := ConfigureNodeAutostart(); err != nil {
		_ = securestore.DeleteAppSession()
		return SessionStatus{}, err
	}
	if err := RestartNode(); err != nil {
		_ = securestore.DeleteAppSession()
		return SessionStatus{}, err
	}
	session, err := securestore.LoadAppSession()
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
	profile, err := securestore.LoadDeviceProfile()
	if err != nil {
		return SessionStatus{}, err
	}
	needsRegistration, err := contentRegistrationRequired(profile)
	if err != nil {
		return SessionStatus{}, err
	}
	if needsRegistration {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		tailnet, err := tailscale.New()
		if err != nil {
			return SessionStatus{}, err
		}
		status, err := tailnet.Status(ctx)
		if err != nil {
			return SessionStatus{}, err
		}
		var existing *managed.Profile
		if profile.ManagedContent != nil && managed.ValidateProfile(*profile.ManagedContent) == nil {
			existing = profile.ManagedContent
		}
		content, err := prepareManagedContent(ctx, existing)
		if err != nil {
			return SessionStatus{}, err
		}
		if _, err := s.client.RegisterCurrentNode(ctx, status, &content); err != nil {
			return SessionStatus{}, err
		}
		if err := ConfigureNodeAutostart(); err != nil {
			return SessionStatus{}, err
		}
		if err := RestartNode(); err != nil {
			return SessionStatus{}, err
		}
	}
	if err := RepairNodeAutostart(); err != nil {
		return SessionStatus{}, err
	}
	return SessionStatus{LoggedIn: true, ControlURL: session.ControlURL, ExpiresAt: session.AccessExpiresAt}, nil
}

func contentRegistrationRequired(profile securestore.DeviceProfile) (bool, error) {
	if profile.ManagedContent == nil || managed.ValidateProfile(*profile.ManagedContent) != nil {
		return true, nil
	}
	publicKey, err := base64.RawURLEncoding.DecodeString(profile.ControlPublicKey)
	if err != nil || len(publicKey) != ed25519.PublicKeySize {
		return false, errors.New("设备安全档案中的 Control 公钥无效")
	}
	var config protocol.SignedDeviceConfig
	if err := secure.VerifyJWS(profile.SignedConfig, ed25519.PublicKey(publicKey), profile.ControlKeyID, &config); err != nil {
		return false, fmt.Errorf("验证设备签名配置失败: %w", err)
	}
	directories, modules, err := DiscoverDefaultContent()
	if err != nil {
		return false, err
	}
	return !slices.Equal(config.SharedDirectories, directories) || !slices.Equal(config.Modules, modules), nil
}

func prepareManagedContent(ctx context.Context, existing *managed.Profile) (managed.Profile, error) {
	publicKey, err := decodeUpdatePublicKey(buildinfo.UpdatePublicKey)
	if err != nil || len(publicKey) != ed25519.PublicKeySize {
		return managed.Profile{}, errors.New("应用未内置有效的组件清单签名公钥")
	}
	stateDir, err := nodeStateDirectory()
	if err != nil {
		return managed.Profile{}, err
	}
	return managed.Prepare(ctx, stateDir, buildinfo.ComponentManifestURL, ed25519.PublicKey(publicKey), existing)
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
	if err := s.client.AuthenticatedJSON(ctx, http.MethodGet, "/api/devices", nil, &response, http.StatusOK); err != nil {
		return nil, err
	}
	return response.Devices, nil
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
	path := "/api/devices/" + url.PathEscape(deviceID) + "/tickets"
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
	return LocalStatus{Online: status.Online, TailnetIP: status.TailscaleIP, Connection: status.Connection}, nil
}
