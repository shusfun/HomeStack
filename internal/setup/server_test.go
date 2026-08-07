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

type fakeHelper struct {
	status   Status
	prepared *Configuration
}

func (f *fakeHelper) Status(context.Context) (Status, error) { return f.status, nil }
func (f *fakeHelper) Prepare(_ context.Context, config Configuration) (Status, error) {
	f.prepared = &config
	f.status = Status{Phase: PhasePocketID, Config: &config, UpdatedAt: time.Now().UTC()}
	return f.status, nil
}
func (f *fakeHelper) Finalize(context.Context) (Status, error) {
	f.status.Phase = PhaseFinalize
	return f.status, nil
}

func TestSetupSessionRequiresLoopbackHTTPSProxyAndToken(t *testing.T) {
	server := newTestServer(t, &fakeHelper{status: Status{Phase: PhaseInfrastructure}})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/setup/session", strings.NewReader(`{"token":"setup-token"}`))
	request.RemoteAddr = "127.0.0.1:1234"
	request.Host = "app.example.com"
	request.Header.Set("X-Forwarded-Proto", "https")
	response := httptest.NewRecorder()
	server.Handler(nil).ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || !strings.Contains(response.Header().Get("Set-Cookie"), "Secure") {
		t.Fatalf("合法 Setup 令牌应建立安全会话: %d %s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/api/v1/setup/session", strings.NewReader(`{"token":"setup-token"}`))
	request.RemoteAddr = "192.0.2.10:1234"
	request.Host = "app.example.com"
	request.Header.Set("X-Forwarded-Proto", "https")
	response = httptest.NewRecorder()
	server.Handler(nil).ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("非回环代理必须被拒绝: %d", response.Code)
	}
}

func TestValidateConfigurationRejectsAmbiguousHostsAndPrivateIP(t *testing.T) {
	base := Configuration{ControlHost: "app.example.com", PocketHost: "id.example.com", MeshHost: "mesh.example.com", TailHost: "tail.example.com", PublicIPv4: "203.0.113.8"}
	if err := ValidateConfiguration(base); err != nil {
		t.Fatalf("合法配置被拒绝: %v", err)
	}
	base.MeshHost = base.ControlHost
	if err := ValidateConfiguration(base); err == nil {
		t.Fatal("重复域名必须被拒绝")
	}
	base.MeshHost = "mesh.example.com"
	base.PublicIPv4 = "10.0.0.1"
	if err := ValidateConfiguration(base); err == nil {
		t.Fatal("私网 IPv4 必须被拒绝")
	}
}

func TestStatusHidesConfigurationBeforeAuthentication(t *testing.T) {
	helper := &fakeHelper{status: Status{Phase: PhasePocketID, Config: &Configuration{ControlHost: "app.example.com"}, UpdatedAt: time.Now().UTC()}}
	server := newTestServer(t, helper)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/setup/status", nil)
	response := httptest.NewRecorder()
	server.Handler(nil).ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"phase":"token"`) || strings.Contains(response.Body.String(), "app.example.com") {
		t.Fatalf("未认证状态泄露 Setup 配置: %d %s", response.Code, response.Body.String())
	}
}

func TestSetupTokenIsConsumedAndSessionSurvivesRestart(t *testing.T) {
	directory := t.TempDir()
	tokenPath := filepath.Join(directory, "token.sha256")
	sessionPath := filepath.Join(directory, "session.json")
	digest := sha256.Sum256([]byte("setup-token"))
	if err := os.WriteFile(tokenPath, []byte(hex.EncodeToString(digest[:])+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	helper := &fakeHelper{status: Status{Phase: PhaseInfrastructure, UpdatedAt: now}}
	options := ServerOptions{TokenHashPath: tokenPath, SessionPath: sessionPath, Helper: helper, Now: func() time.Time { return now }, Random: bytes.NewReader(make([]byte, 64))}
	server, err := NewServer(options)
	if err != nil {
		t.Fatal(err)
	}
	request := validSetupRequest(http.MethodPost, "/api/v1/setup/session", `{"token":"setup-token"}`)
	response := httptest.NewRecorder()
	server.Handler(nil).ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("首次兑换 Setup 令牌失败: %d %s", response.Code, response.Body.String())
	}
	if _, err := os.Stat(tokenPath); !os.IsNotExist(err) {
		t.Fatalf("令牌摘要未在兑换后删除: %v", err)
	}
	if _, err := os.Stat(sessionPath); err != nil {
		t.Fatalf("Setup 会话摘要未持久化: %v", err)
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Expires.Sub(now) != 24*time.Hour {
		t.Fatalf("Setup 会话有效期不是固定 24 小时: %+v", cookies)
	}

	restarted, err := NewServer(options)
	if err != nil {
		t.Fatalf("Setup 服务未能从持久化会话恢复: %v", err)
	}
	statusRequest := validSetupRequest(http.MethodGet, "/api/v1/setup/status", "")
	statusRequest.AddCookie(cookies[0])
	statusResponse := httptest.NewRecorder()
	restarted.Handler(nil).ServeHTTP(statusResponse, statusRequest)
	if statusResponse.Code != http.StatusOK || !strings.Contains(statusResponse.Body.String(), `"phase":"infrastructure"`) {
		t.Fatalf("重启后 Setup 会话失效: %d %s", statusResponse.Code, statusResponse.Body.String())
	}
	replay := httptest.NewRecorder()
	restarted.Handler(nil).ServeHTTP(replay, validSetupRequest(http.MethodPost, "/api/v1/setup/session", `{"token":"setup-token"}`))
	if replay.Code != http.StatusLocked || !strings.Contains(replay.Body.String(), "token_consumed") {
		t.Fatalf("已消费令牌可被重复使用: %d %s", replay.Code, replay.Body.String())
	}
	now = now.Add(24 * time.Hour)
	expired := httptest.NewRecorder()
	restarted.Handler(nil).ServeHTTP(expired, statusRequest)
	if expired.Code != http.StatusOK || !strings.Contains(expired.Body.String(), `"phase":"token"`) {
		t.Fatalf("过期 Setup 会话仍可使用: %d %s", expired.Code, expired.Body.String())
	}
}

func TestSetupTokenAttemptsAreRateLimited(t *testing.T) {
	server := newTestServer(t, &fakeHelper{status: Status{Phase: PhaseInfrastructure}})
	for attempt := 1; attempt <= 6; attempt++ {
		response := httptest.NewRecorder()
		server.Handler(nil).ServeHTTP(response, validSetupRequest(http.MethodPost, "/api/v1/setup/session", `{"token":"wrong"}`))
		expected := http.StatusUnauthorized
		if attempt == 6 {
			expected = http.StatusTooManyRequests
		}
		if response.Code != expected {
			t.Fatalf("第 %d 次令牌尝试状态错误: got=%d want=%d", attempt, response.Code, expected)
		}
	}
}

func TestSetupTokenConcurrentExchangeSucceedsOnce(t *testing.T) {
	server := newTestServer(t, &fakeHelper{status: Status{Phase: PhaseInfrastructure}})
	const requests = 8
	codes := make(chan int, requests)
	var group sync.WaitGroup
	for index := 0; index < requests; index++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			response := httptest.NewRecorder()
			request := validSetupRequest(http.MethodPost, "/api/v1/setup/session", `{"token":"setup-token"}`)
			request.Header.Set("X-Real-IP", fmt.Sprintf("198.51.100.%d", index+1))
			server.Handler(nil).ServeHTTP(response, request)
			codes <- response.Code
		}(index)
	}
	group.Wait()
	close(codes)
	succeeded := 0
	locked := 0
	for code := range codes {
		switch code {
		case http.StatusNoContent:
			succeeded++
		case http.StatusLocked:
			locked++
		default:
			t.Fatalf("并发兑换返回意外状态: %d", code)
		}
	}
	if succeeded != 1 || locked != requests-1 {
		t.Fatalf("并发兑换结果错误: success=%d locked=%d", succeeded, locked)
	}
}

func validSetupRequest(method, target, body string) *http.Request {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	request.RemoteAddr = "127.0.0.1:1234"
	request.Host = "app.example.com"
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
