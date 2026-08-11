package desktop

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"slices"
	"sync"
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
	client             *APIClient
	updates            *UpdateService
	identityLock       sync.Mutex
	managedContentLock sync.Mutex
	managedStatusMu    sync.RWMutex
	managedStatus      ManagedContentStatus
	managedCancel      context.CancelFunc
	managedDone        chan struct{}
	managedRunner      func(context.Context) error
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

const managedContentPreparationTimeout = 60 * time.Minute

func NewService() *Service {
	return &Service{client: &APIClient{OpenURL: openSystemURL}, updates: newUpdateService(), managedStatus: newManagedContentStatus()}
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
	s.identityLock.Lock()
	defer s.identityLock.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), managedContentPreparationTimeout)
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
	if _, err := s.client.RegisterCurrentNodeCore(ctx, status); err != nil {
		_ = securestore.DeleteAppSession()
		return SessionStatus{}, err
	}
	result := sessionStatus(session)
	s.startManagedContentAfterActivation()
	return result, nil
}

func (s *Service) Activate(controlURL, code string) (SessionStatus, error) {
	s.identityLock.Lock()
	defer s.identityLock.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), managedContentPreparationTimeout)
	defer cancel()
	tailnet, err := tailscale.New()
	if err != nil {
		return SessionStatus{}, err
	}
	status, err := tailnet.Status(ctx)
	if err != nil {
		return SessionStatus{}, err
	}
	if _, err := s.client.ActivateCurrentNode(ctx, controlURL, code, status, nil); err != nil {
		return SessionStatus{}, err
	}
	session, err := securestore.LoadAppSession()
	if err != nil {
		return SessionStatus{}, err
	}
	result := sessionStatus(session)
	s.startManagedContentAfterActivation()
	return result, nil
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
	if _, err := securestore.LoadCoreDeviceProfile(); err != nil {
		return SessionStatus{}, err
	}
	return sessionStatus(session), nil
}

func sessionStatus(session securestore.AppSession) SessionStatus {
	return SessionStatus{LoggedIn: true, ControlURL: session.ControlURL, ExpiresAt: session.AccessExpiresAt}
}

func (s *Service) startManagedContentAfterActivation() {
	if _, err := s.startManagedContentPreparation(true); err != nil {
		s.finishManagedPreparation("error", "error", err.Error())
		log.Printf("启动托管组件准备失败: %v", err)
	}
}

func contentRegistrationRequired(profile securestore.DeviceProfile) (bool, error) {
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

func prepareManagedContent(ctx context.Context, existing *managed.Profile, report managed.ProgressFunc) (managed.Profile, error) {
	publicKey, err := decodeUpdatePublicKey(buildinfo.UpdatePublicKey)
	if err != nil || len(publicKey) != ed25519.PublicKeySize {
		return managed.Profile{}, errors.New("应用未内置有效的组件清单签名公钥")
	}
	stateDir, err := nodeStateDirectory()
	if err != nil {
		return managed.Profile{}, err
	}
	return managed.PrepareWithProgress(ctx, stateDir, buildinfo.ComponentManifestURL, ed25519.PublicKey(publicKey), existing, report)
}

func (s *Service) prepareAndStartManagedContent(parent context.Context, existing *managed.Profile) (managed.Profile, error) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	content, err := prepareManagedContent(ctx, existing, s.reportManagedProgress)
	if err != nil {
		return managed.Profile{}, err
	}
	s.setManagedStage("saving")
	profile, err := securestore.LoadDeviceProfile()
	if err != nil {
		return managed.Profile{}, err
	}
	profile.ManagedContent = &content
	if err := securestore.SaveDeviceProfile(profile); err != nil {
		return managed.Profile{}, err
	}
	s.setManagedStage("configuring")
	if err := ConfigureNodeAutostart(); err != nil {
		return managed.Profile{}, err
	}
	s.setManagedStage("starting")
	if err := RestartNode(); err != nil {
		return managed.Profile{}, err
	}
	s.setManagedStage("health")
	healthContext, healthCancel := context.WithTimeout(ctx, 2*time.Minute)
	defer healthCancel()
	if err := waitNodeHealth(healthContext); err != nil {
		return managed.Profile{}, err
	}
	s.setManagedReadyFromProfile(content)
	return content, nil
}

func (s *Service) EnsureManagedContentPreparation() (ManagedContentStatus, error) {
	return s.startManagedContentPreparation(false)
}

func (s *Service) startManagedContentPreparation(force bool) (ManagedContentStatus, error) {
	hasSession, err := securestore.HasAppSession()
	if err != nil {
		return s.ManagedContentStatus(), err
	}
	if !hasSession {
		return s.ManagedContentStatus(), errors.New("尚未完成登录，不能继续准备组件")
	}
	if _, err := securestore.LoadCoreDeviceProfile(); err != nil {
		return s.ManagedContentStatus(), err
	}
	s.managedStatusMu.Lock()
	current := cloneManagedStatus(s.managedStatus)
	if s.managedCancel != nil {
		s.managedStatusMu.Unlock()
		if current.State == "preparing" {
			return current, nil
		}
		return current, errors.New("组件准备任务正在停止，请稍后重试")
	}
	if !force && current.State != "idle" {
		s.managedStatusMu.Unlock()
		return current, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), managedContentPreparationTimeout)
	status := newManagedContentStatus()
	status.State, status.Phase = "preparing", "manifest"
	s.managedStatus = status
	s.managedCancel = cancel
	done := make(chan struct{})
	s.managedDone = done
	current = cloneManagedStatus(status)
	s.managedStatusMu.Unlock()
	emitManagedStatus(current)
	go s.runManagedContentPreparation(ctx, cancel, done)
	return current, nil
}

func (s *Service) runManagedContentPreparation(ctx context.Context, cancel context.CancelFunc, done chan struct{}) {
	defer close(done)
	defer cancel()
	s.managedContentLock.Lock()
	defer s.managedContentLock.Unlock()
	runner := s.managedRunner
	if runner == nil {
		runner = s.runManagedContent
	}
	if err := runner(ctx); err != nil {
		s.finishManagedPreparation("error", "error", err.Error())
		log.Printf("托管组件准备失败: %v", err)
		return
	}
	s.finishManagedPreparation("ready", "ready", "")
}

func (s *Service) runManagedContent(ctx context.Context) error {
	profile, err := securestore.LoadDeviceProfile()
	if err != nil {
		return err
	}
	var existing *managed.Profile
	if profile.ManagedContent != nil && managed.ValidateProfile(*profile.ManagedContent) == nil {
		existing = profile.ManagedContent
	}
	needsRegistration, err := contentRegistrationRequired(profile)
	if err != nil {
		return err
	}
	if needsRegistration {
		tailnet, err := tailscale.New()
		if err != nil {
			return err
		}
		status, err := tailnet.Status(ctx)
		if err != nil {
			return err
		}
		if _, err := s.client.RegisterCurrentNode(ctx, status, existing); err != nil {
			return err
		}
	}
	if existing == nil {
		_, err := s.prepareAndStartManagedContent(ctx, nil)
		return err
	}
	healthContext, healthCancel := context.WithTimeout(ctx, 2*time.Minute)
	defer healthCancel()
	s.beginManagedHealthCheck(*existing, healthCancel)
	if err := RepairNodeAutostart(); err != nil {
		return err
	}
	if err := waitNodeHealth(healthContext); err != nil {
		return err
	}
	s.setManagedReadyFromProfile(*existing)
	return nil
}

func (s *Service) ResumeManagedContentPreparation() (SessionStatus, error) {
	if _, err := s.startManagedContentPreparation(true); err != nil {
		return SessionStatus{}, err
	}
	session, err := securestore.LoadAppSession()
	if err != nil {
		return SessionStatus{}, err
	}
	return sessionStatus(session), nil
}

func (s *Service) Logout() error {
	s.identityLock.Lock()
	defer s.identityLock.Unlock()

	s.managedStatusMu.Lock()
	cancel := s.managedCancel
	done := s.managedDone
	s.managedStatusMu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
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
