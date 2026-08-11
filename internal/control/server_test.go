package control

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/wangshangbin/homestack/internal/controlupdate"
	"github.com/wangshangbin/homestack/internal/protocol"
)

type testControlUpdater struct {
	status controlupdate.Status
	calls  []string
}

func (u *testControlUpdater) Status() controlupdate.Status { return u.status }
func (u *testControlUpdater) Check(context.Context) (controlupdate.Status, error) {
	u.calls = append(u.calls, "check")
	u.status.State = "available"
	return u.status, nil
}
func (u *testControlUpdater) Download(context.Context) (controlupdate.Status, error) {
	u.calls = append(u.calls, "download")
	u.status.State = "ready"
	return u.status, nil
}
func (u *testControlUpdater) Install(context.Context) (controlupdate.Status, error) {
	u.calls = append(u.calls, "install")
	u.status.State = "installing"
	return u.status, nil
}

type testAuthenticator struct {
	identity  Identity
	err       error
	providers []ProviderMetadata
}

func (a testAuthenticator) Authenticate(context.Context, *http.Request) (Identity, error) {
	return a.identity, a.err
}
func (a testAuthenticator) Metadata() []ProviderMetadata { return a.providers }
func (a testAuthenticator) IssueAppTokens(string) (AppTokens, error) {
	return AppTokens{AccessToken: "access", RefreshToken: "refresh", ExpiresAt: time.Now().Add(time.Hour), RefreshExpiresAt: time.Now().Add(24 * time.Hour), TokenType: "Bearer"}, nil
}

func TestCompletedControlPermanentlyLocksSetupAPI(t *testing.T) {
	server, _ := newTestControlServer(t, time.Now().UTC())
	for _, target := range []struct{ method, path string }{{http.MethodGet, "/api/setup/status"}, {http.MethodPost, "/api/setup/session"}, {http.MethodPost, "/api/setup/prepare"}, {http.MethodPost, "/api/setup/finalize"}} {
		response := httptest.NewRecorder()
		server.Handler(nil).ServeHTTP(response, httptest.NewRequest(target.method, target.path, nil))
		if response.Code != http.StatusLocked || !strings.Contains(response.Body.String(), "setup_locked") {
			t.Fatalf("未锁定 %s: %d %s", target.path, response.Code, response.Body.String())
		}
	}
}

func TestMetaBootstrapsLoginWithoutAuthenticationError(t *testing.T) {
	server, owner := newTestControlServer(t, time.Now().UTC())
	authenticated := httptest.NewRecorder()
	server.Handler(nil).ServeHTTP(authenticated, httptest.NewRequest(http.MethodGet, "/api/meta", nil))
	if authenticated.Code != http.StatusOK || !strings.Contains(authenticated.Body.String(), owner.Subject) {
		t.Fatalf("有效会话启动信息错误: %d %s", authenticated.Code, authenticated.Body.String())
	}

	server.authenticator = testAuthenticator{err: ErrUnauthenticated, providers: []ProviderMetadata{{ID: "github", Label: "GitHub"}}}
	anonymous := httptest.NewRecorder()
	server.Handler(nil).ServeHTTP(anonymous, httptest.NewRequest(http.MethodGet, "/api/meta", nil))
	if anonymous.Code != http.StatusOK || !strings.Contains(anonymous.Body.String(), `"me":null`) || anonymous.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("匿名启动信息错误: %d %s", anonymous.Code, anonymous.Body.String())
	}

	protected := httptest.NewRecorder()
	server.Handler(nil).ServeHTTP(protected, httptest.NewRequest(http.MethodGet, "/api/me", nil))
	if protected.Code != http.StatusUnauthorized {
		t.Fatalf("受保护的当前用户接口未保持鉴权: %d", protected.Code)
	}
}

func TestControlUpdateAPIRequiresOwnerBrowserAndRunsUpdater(t *testing.T) {
	server, _ := newTestControlServer(t, time.Now().UTC())
	updater := &testControlUpdater{status: controlupdate.Status{State: "idle", CurrentVersion: "1.2.3", Signature: "等待校验"}}
	server.controlUpdater = updater

	status := httptest.NewRecorder()
	server.Handler(nil).ServeHTTP(status, httptest.NewRequest(http.MethodGet, "/api/system/updates/status", nil))
	if status.Code != http.StatusOK || !strings.Contains(status.Body.String(), `"current_version":"1.2.3"`) {
		t.Fatalf("Control 更新状态错误: %d %s", status.Code, status.Body.String())
	}

	rejected := httptest.NewRecorder()
	server.Handler(nil).ServeHTTP(rejected, httptest.NewRequest(http.MethodPost, "/api/system/updates/check", nil))
	if rejected.Code != http.StatusForbidden {
		t.Fatalf("缺少同源信息的更新操作未被拒绝: %d", rejected.Code)
	}

	for _, target := range []struct {
		path string
		code int
	}{{"/api/system/updates/check", http.StatusOK}, {"/api/system/updates/download", http.StatusOK}, {"/api/system/updates/install", http.StatusAccepted}} {
		request := httptest.NewRequest(http.MethodPost, target.path, nil)
		request.Header.Set("Origin", "https://home.example.com")
		response := httptest.NewRecorder()
		server.Handler(nil).ServeHTTP(response, request)
		if response.Code != target.code {
			t.Fatalf("更新操作 %s 失败: %d %s", target.path, response.Code, response.Body.String())
		}
	}
	if strings.Join(updater.calls, ",") != "check,download,install" {
		t.Fatalf("更新调用顺序错误: %v", updater.calls)
	}
}

func TestActivationRegistersNodeAndCannotReplay(t *testing.T) {
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	server, owner := newTestControlServer(t, now)
	code, _, err := server.activations.Create(owner.Subject)
	if err != nil {
		t.Fatal(err)
	}
	_, deviceKey, _ := ed25519.GenerateKey(rand.Reader)
	encryptionKey, _ := ecdh.X25519().GenerateKey(rand.Reader)
	node := protocol.NodeRegistration{Name: "mac", Platform: "darwin", Architecture: "arm64", TailscaleIP: "100.64.0.8", MagicDNS: "mac.tail-name.ts.net", DevicePublicKey: base64.RawURLEncoding.EncodeToString(deviceKey.Public().(ed25519.PublicKey)), EncryptionPublicKey: base64.RawURLEncoding.EncodeToString(encryptionKey.PublicKey().Bytes())}
	body, _ := json.Marshal(map[string]any{"code": code, "node": node})
	response := httptest.NewRecorder()
	server.Handler(nil).ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/auth/app/activate", bytes.NewReader(body)))
	if response.Code != http.StatusCreated || server.devices.Count() != 1 {
		t.Fatalf("激活失败: %d %s", response.Code, response.Body.String())
	}
	replay := httptest.NewRecorder()
	server.Handler(nil).ServeHTTP(replay, httptest.NewRequest(http.MethodPost, "/api/auth/app/activate", bytes.NewReader(body)))
	if replay.Code != http.StatusUnauthorized {
		t.Fatalf("激活码可重放: %d", replay.Code)
	}
}

func TestCreateTicketUsesMagicDNSNodePort(t *testing.T) {
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	server, owner := newTestControlServer(t, now)
	_, err := server.devices.Register(DeviceRecord{ID: "device-1", Name: "NAS", OwnerSubject: owner.Subject, DevicePublicKey: "key", AgentURL: "https://nas.tail-name.ts.net:19443", CreatedAt: now}, "device-token", "tail-name.ts.net")
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/devices/device-1/tickets", nil)
	request.Header.Set("Origin", "https://home.example.com")
	response := httptest.NewRecorder()
	server.Handler(nil).ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("票据失败: %d %s", response.Code, response.Body.String())
	}
	var ticket ticketResponse
	if err := json.Unmarshal(response.Body.Bytes(), &ticket); err != nil {
		t.Fatal(err)
	}
	target, _ := url.Parse(ticket.URL)
	if target.Host != "nas.tail-name.ts.net:19443" || target.Path != "/access" || target.Query().Get("ticket") == "" {
		t.Fatalf("票据目标错误: %s", ticket.URL)
	}
}

func newTestControlServer(t *testing.T, now time.Time) (*Server, Identity) {
	t.Helper()
	owners, err := OpenOwnerStore("")
	if err != nil {
		t.Fatal(err)
	}
	owner, err := owners.AuthenticateOrClaim(ExternalIdentity{Provider: "github", Subject: "owner-subject", Email: "owner@example.com", Name: "Owner", EmailVerified: true})
	if err != nil {
		t.Fatal(err)
	}
	_, privateKey, _ := ed25519.GenerateKey(rand.Reader)
	auth := testAuthenticator{identity: owner, providers: []ProviderMetadata{{ID: "github", Label: "GitHub"}}}
	server, err := NewServer(ServerOptions{Authenticator: auth, Owners: owners, Devices: NewMemoryDeviceStore(), SigningKey: privateKey, SigningKeyID: "control-test", PublicURL: "https://home.example.com", Now: func() time.Time { return now }, Random: bytes.NewReader(make([]byte, 4096))})
	if err != nil {
		t.Fatal(err)
	}
	return server, owner
}
