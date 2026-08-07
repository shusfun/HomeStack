package control

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/wangshangbin/homestack/internal/invite"
	"github.com/wangshangbin/homestack/internal/maintenance"
)

type testAuthenticator struct {
	identity  Identity
	err       error
	providers []ProviderMetadata
}

func (a testAuthenticator) Authenticate(context.Context, *http.Request) (Identity, error) {
	return a.identity, a.err
}

func (a testAuthenticator) Metadata() []ProviderMetadata { return a.providers }

type testHeadscale struct{}

func (testHeadscale) CreateSingleUseKey(context.Context, string) (string, error) {
	return "single-use-key", nil
}

type maintenanceTestAuthenticator struct {
	testAuthenticator
	grant bool
}

func (a *maintenanceTestAuthenticator) ConsumeMaintenanceGrant(*http.Request, string) bool {
	granted := a.grant
	a.grant = false
	return granted
}

type testMaintenance struct {
	config maintenance.Configuration
	status maintenance.Status
	called int
}

func (m *testMaintenance) Configuration(context.Context) (maintenance.Configuration, error) {
	return m.config, nil
}

func (m *testMaintenance) Status(context.Context) (maintenance.Status, error) { return m.status, nil }

func (m *testMaintenance) Reconfigure(_ context.Context, config maintenance.Configuration) (maintenance.Status, error) {
	m.called++
	m.config = config
	return maintenance.Status{Phase: maintenance.PhasePreflight, Target: &config}, nil
}

func TestCompletedControlPermanentlyLocksSetupAPI(t *testing.T) {
	server, _ := newTestControlServer(t, time.Now().UTC(), nil)
	for _, target := range []struct{ method, path string }{{http.MethodGet, "/api/v1/setup/status"}, {http.MethodPost, "/api/v1/setup/session"}, {http.MethodPost, "/api/v1/setup/prepare"}, {http.MethodPost, "/api/v1/setup/finalize"}} {
		response := httptest.NewRecorder()
		server.Handler(nil).ServeHTTP(response, httptest.NewRequest(target.method, target.path, nil))
		if response.Code != http.StatusLocked || !strings.Contains(response.Body.String(), "setup_locked") {
			t.Fatalf("正式 Control 未锁定 %s %s: %d %s", target.method, target.path, response.Code, response.Body.String())
		}
	}
}

func TestReconfigureRequiresBrowserOriginReauthAndConfirmation(t *testing.T) {
	server, owner := newTestControlServer(t, time.Now().UTC(), nil)
	auth := &maintenanceTestAuthenticator{testAuthenticator: testAuthenticator{identity: owner}, grant: true}
	helper := &testMaintenance{}
	server.authenticator = auth
	server.maintenance = helper
	body := `{"control_host":"new.example.com","pocket_host":"id.example.com","mesh_host":"mesh.example.com","tail_host":"tail.example.com","public_ipv4":"203.0.113.8","confirmation":"new.example.com"}`

	request := httptest.NewRequest(http.MethodPost, "/api/v1/system/reconfigure", strings.NewReader(body))
	request.Header.Set("Origin", "https://control.example.com")
	response := httptest.NewRecorder()
	server.Handler(nil).ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || helper.called != 1 {
		t.Fatalf("合法域名迁移未被接受: %d %s called=%d", response.Code, response.Body.String(), helper.called)
	}

	response = httptest.NewRecorder()
	server.Handler(nil).ServeHTTP(response, request.Clone(context.Background()))
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "reauthentication_required") {
		t.Fatalf("迁移重新认证授权可重复使用: %d %s", response.Code, response.Body.String())
	}

	auth.grant = true
	bearer := httptest.NewRequest(http.MethodPost, "/api/v1/system/reconfigure", strings.NewReader(body))
	bearer.Header.Set("Origin", "https://control.example.com")
	bearer.Header.Set("Authorization", "Bearer app-token")
	response = httptest.NewRecorder()
	server.Handler(nil).ServeHTTP(response, bearer)
	if response.Code != http.StatusForbidden || helper.called != 1 {
		t.Fatalf("Bearer 请求绕过浏览器限制: %d %s", response.Code, response.Body.String())
	}
}

func TestCreateTicketRequiresOriginAndUsesFixedAgentURL(t *testing.T) {
	now := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	server, owner := newTestControlServer(t, now, nil)
	server.authenticator = testAuthenticator{identity: owner}
	if err := server.devices.Add(DeviceRecord{
		ID: "device-1", Name: "NAS", OwnerSubject: owner.Subject,
		AgentURL: "https://nas.example.ts.net:9443", CreatedAt: now,
	}, "device-token"); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, "/api/v1/devices/device-1/tickets", nil)
	response := httptest.NewRecorder()
	server.Handler(nil).ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("缺少 Origin 的浏览器票据请求应返回 403，实际 %d", response.Code)
	}

	request = httptest.NewRequest(http.MethodPost, "/api/v1/devices/device-1/tickets", nil)
	request.Header.Set("Origin", "https://control.example.com")
	response = httptest.NewRecorder()
	server.Handler(nil).ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("合法票据请求失败: %d %s", response.Code, response.Body.String())
	}
	var ticket ticketResponse
	if err := json.Unmarshal(response.Body.Bytes(), &ticket); err != nil {
		t.Fatal(err)
	}
	if !ticket.ExpiresAt.Equal(now.Add(30 * time.Second)) {
		t.Fatalf("票据必须在 30 秒后过期，实际 %s", ticket.ExpiresAt)
	}
	target, err := url.Parse(ticket.URL)
	if err != nil {
		t.Fatal(err)
	}
	if target.Scheme != "https" || target.Host != "nas.example.ts.net:9443" || target.Path != "/access" || target.Query().Get("ticket") == "" {
		t.Fatalf("票据目标不是固定 Agent 地址: %s", ticket.URL)
	}
}

func TestCreateTicketRejectsWrongOwner(t *testing.T) {
	server, owner := newTestControlServer(t, time.Now().UTC(), nil)
	server.authenticator = testAuthenticator{identity: Identity{Subject: "another-owner", Email: "other@example.com"}}
	if err := server.devices.Add(DeviceRecord{ID: "device-1", OwnerSubject: owner.Subject, AgentURL: "https://nas.example.ts.net:9443"}, "device-token"); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/devices/device-1/tickets", nil)
	request.Header.Set("Authorization", "Bearer app-token")
	response := httptest.NewRecorder()
	server.Handler(nil).ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("错误所有者应返回 403，实际 %d", response.Code)
	}
}

func TestOpenDeviceLoginRedirectCannotUseArbitraryReturnURL(t *testing.T) {
	server, _ := newTestControlServer(t, time.Now().UTC(), errors.New("未登录"))
	request := httptest.NewRequest(http.MethodGet, "/devices/device-1/open?return=https://evil.example", nil)
	response := httptest.NewRecorder()
	server.Handler(nil).ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("未登录设备入口应跳转登录，实际 %d", response.Code)
	}
	location, err := url.Parse(response.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if location.Path != "/auth/login/pocket" || location.Query().Get("return") != "/devices/device-1/open" || strings.Contains(location.String(), "evil.example") {
		t.Fatalf("登录跳转使用了非固定返回地址: %s", location.String())
	}
}

func newTestControlServer(t *testing.T, now time.Time, authErr error) (*Server, Identity) {
	t.Helper()
	owners, err := OpenOwnerStore("")
	if err != nil {
		t.Fatal(err)
	}
	owner, err := owners.AuthenticateOrClaim(ExternalIdentity{
		Provider: "pocket", Subject: "owner-subject", Email: "owner@example.com", Name: "Owner", EmailVerified: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	auth := testAuthenticator{identity: owner, err: authErr, providers: []ProviderMetadata{{ID: "pocket", Label: "Pocket ID"}}}
	server, err := NewServer(ServerOptions{
		Authenticator: auth, Owners: owners,
		Invites: invite.NewMemory(func() time.Time { return now }, bytes.NewReader(make([]byte, 1024))),
		Devices: NewMemoryDeviceStore(), Headscale: testHeadscale{}, SigningKey: privateKey,
		SigningKeyID: "control-test", PublicURL: "https://control.example.com", HeadscaleURL: "https://mesh.example.com",
		Now: func() time.Time { return now }, Random: bytes.NewReader(make([]byte, 1024)),
	})
	if err != nil {
		t.Fatal(err)
	}
	return server, owner
}
