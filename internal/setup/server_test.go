package setup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeHelper struct{ status Status }

func (f *fakeHelper) Status(context.Context) (Status, error) { return f.status, nil }
func (f *fakeHelper) Prepare(_ context.Context, config Configuration) (Status, error) {
	public := PublicConfigurationFor(config)
	f.status = Status{Phase: PhaseIdentity, Config: &public, UpdatedAt: time.Now().UTC()}
	return f.status, nil
}
func (f *fakeHelper) Finalize(context.Context) (Status, error) {
	f.status.Phase = PhaseFinalize
	return f.status, nil
}

func TestSetupSessionRequiresLoopbackHTTPSProxyAndToken(t *testing.T) {
	server := newTestServer(t, &fakeHelper{status: Status{Phase: PhaseDomain}})
	request := validSetupRequest(http.MethodPost, "/api/setup/session", `{"token":"setup-token"}`)
	response := httptest.NewRecorder()
	server.Handler(nil).ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || !strings.Contains(response.Header().Get("Set-Cookie"), "Secure") {
		t.Fatalf("合法令牌未建立安全会话: %d %s", response.Code, response.Body.String())
	}
	request = validSetupRequest(http.MethodPost, "/api/setup/session", `{"token":"setup-token"}`)
	request.RemoteAddr = "192.0.2.10:1234"
	response = httptest.NewRecorder()
	server.Handler(nil).ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("非回环代理必须被拒绝: %d", response.Code)
	}
}

func TestValidateConfigurationAllowsOneOrTwoProviders(t *testing.T) {
	config := Configuration{PublicHost: "home.example.com", Providers: map[string]ProviderCredentials{"google": {ClientID: "client", ClientSecret: "secret"}}}
	if err := ValidateConfiguration(config); err != nil {
		t.Fatal(err)
	}
	config.Providers["github"] = ProviderCredentials{ClientID: "github-client", ClientSecret: "github-secret"}
	if err := ValidateConfiguration(config); err != nil {
		t.Fatal(err)
	}
	config.Providers["pocket"] = ProviderCredentials{ClientID: "client", ClientSecret: "secret"}
	if err := ValidateConfiguration(config); err == nil {
		t.Fatal("非 Google/GitHub 登录必须被拒绝")
	}
	delete(config.Providers, "pocket")
	config.Providers["github"] = ProviderCredentials{ClientID: "github-client"}
	if err := ValidateConfiguration(config); err == nil {
		t.Fatal("空 Secret 必须被拒绝")
	}
}

func TestStatusNeverReturnsClientSecret(t *testing.T) {
	helper := &fakeHelper{status: Status{Phase: PhaseIdentity, Config: &PublicConfiguration{PublicHost: "home.example.com", Providers: []PublicProviderConfiguration{{ID: "github", ClientID: "client"}}}}}
	server := newTestServer(t, helper)
	response := httptest.NewRecorder()
	server.Handler(nil).ServeHTTP(response, validSetupRequest(http.MethodGet, "/api/setup/status", ""))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"phase":"token"`) || strings.Contains(response.Body.String(), "client") {
		t.Fatalf("未认证状态泄露配置: %s", response.Body.String())
	}
}

func TestSetupTokenConsumedAndSessionSurvivesRestart(t *testing.T) {
	directory := t.TempDir()
	tokenPath := filepath.Join(directory, "token.sha256")
	sessionPath := filepath.Join(directory, "session.json")
	digest := sha256.Sum256([]byte("setup-token"))
	if err := os.WriteFile(tokenPath, []byte(hex.EncodeToString(digest[:])+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	helper := &fakeHelper{status: Status{Phase: PhaseDomain, UpdatedAt: now}}
	options := ServerOptions{TokenHashPath: tokenPath, SessionPath: sessionPath, Helper: helper, Now: func() time.Time { return now }, Random: bytes.NewReader(make([]byte, 64))}
	server, err := NewServer(options)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	server.Handler(nil).ServeHTTP(response, validSetupRequest(http.MethodPost, "/api/setup/session", `{"token":"setup-token"}`))
	if response.Code != http.StatusNoContent {
		t.Fatalf("兑换失败: %d %s", response.Code, response.Body.String())
	}
	if _, err := os.Stat(tokenPath); !os.IsNotExist(err) {
		t.Fatalf("令牌摘要未删除: %v", err)
	}
	restarted, err := NewServer(options)
	if err != nil {
		t.Fatal(err)
	}
	statusRequest := validSetupRequest(http.MethodGet, "/api/setup/status", "")
	statusRequest.AddCookie(response.Result().Cookies()[0])
	statusResponse := httptest.NewRecorder()
	restarted.Handler(nil).ServeHTTP(statusResponse, statusRequest)
	if !strings.Contains(statusResponse.Body.String(), `"phase":"domain"`) {
		t.Fatalf("重启后会话失效: %s", statusResponse.Body.String())
	}
}

func TestSetupTokenConcurrentExchangeSucceedsOnce(t *testing.T) {
	server := newTestServer(t, &fakeHelper{status: Status{Phase: PhaseDomain}})
	const requests = 8
	codes := make(chan int, requests)
	var group sync.WaitGroup
	for index := 0; index < requests; index++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			response := httptest.NewRecorder()
			request := validSetupRequest(http.MethodPost, "/api/setup/session", `{"token":"setup-token"}`)
			request.Header.Set("X-Real-IP", fmt.Sprintf("198.51.100.%d", index+1))
			server.Handler(nil).ServeHTTP(response, request)
			codes <- response.Code
		}(index)
	}
	group.Wait()
	close(codes)
	succeeded := 0
	for code := range codes {
		if code == http.StatusNoContent {
			succeeded++
		} else if code != http.StatusLocked {
			t.Fatalf("意外状态: %d", code)
		}
	}
	if succeeded != 1 {
		t.Fatalf("成功兑换次数: %d", succeeded)
	}
}

func validSetupRequest(method, target, body string) *http.Request {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	request.RemoteAddr = "127.0.0.1:1234"
	request.Host = "home.example.com"
	request.Header.Set("X-Forwarded-Proto", "https")
	return request
}

func newTestServer(t *testing.T, helper Helper) *Server {
	t.Helper()
	digest := sha256.Sum256([]byte("setup-token"))
	directory := t.TempDir()
	path := filepath.Join(directory, "token.sha256")
	if err := os.WriteFile(path, []byte(hex.EncodeToString(digest[:])+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(ServerOptions{TokenHashPath: path, SessionPath: filepath.Join(directory, "session.json"), Helper: helper, Now: func() time.Time { return time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC) }, Random: bytes.NewReader(make([]byte, 64))})
	if err != nil {
		t.Fatal(err)
	}
	return server
}
