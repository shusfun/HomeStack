package agent

import (
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wangshangbin/homestack/internal/protocol"
	"github.com/wangshangbin/homestack/internal/secure"
)

func TestExpiredConfigBlocksTicketRedemption(t *testing.T) {
	server := &Server{configStore: &ConfigStore{current: protocol.SignedDeviceConfig{
		Revision: 1, ExpiresAt: time.Now().UTC().Add(-time.Minute),
	}}}
	request := httptest.NewRequest(http.MethodGet, "/access?ticket=unused", nil)
	response := httptest.NewRecorder()
	server.redeemTicket(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("过期配置应返回 %d，实际为 %d", http.StatusServiceUnavailable, response.Code)
	}
}

func TestTicketRedemptionUsesLaxSecureSessionCookie(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	config := protocol.SignedDeviceConfig{
		DeviceID: "device-1", Revision: 1, ControlURL: "https://control.example.com",
		AgentURL: "https://nas.tail-name.ts.net:19443", ExpiresAt: now.Add(time.Hour),
	}
	sessions, err := OpenSessionStore("", config.DeviceID, config.ControlURL, publicKey, "control-test")
	if err != nil {
		t.Fatal(err)
	}
	claims := protocol.AccessTicketClaims{
		Issuer: config.ControlURL, Subject: "owner-1", DeviceID: config.DeviceID,
		Nonce: "nonce-1", IssuedAt: now, ExpiresAt: now.Add(30 * time.Second),
	}
	ticket, err := secure.SignJWS(privateKey, "control-test", claims)
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{configStore: &ConfigStore{current: config}, sessions: sessions}
	response := httptest.NewRecorder()
	server.redeemTicket(response, httptest.NewRequest(http.MethodGet, "/access?ticket="+ticket, nil))
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/" {
		t.Fatalf("票据兑换应跳转到 Node 首页: %d %s", response.Code, response.Header().Get("Location"))
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != "homestack_session" {
		t.Fatalf("票据兑换未写入 Node 会话 Cookie: %#v", cookies)
	}
	if !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatalf("Node 会话 Cookie 属性不安全或阻止跨站顶层跳转: %#v", cookies[0])
	}
}

func TestDirectDocumentRedirectsAndOnlyMetaIsPublic(t *testing.T) {
	config := protocol.SignedDeviceConfig{
		Revision: 1, ControlURL: "https://control.example.com", AgentURL: "https://nas.tail-name.ts.net:19443",
		ExpiresAt: time.Now().UTC().Add(time.Hour),
	}
	sessions, err := OpenSessionStore("", "device-1", config.ControlURL, ed25519.PublicKey(make([]byte, ed25519.PublicKeySize)), "control-test")
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{deviceID: "device-1", configStore: &ConfigStore{current: config}, sessions: sessions}
	handler := server.Handler(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
	}))

	document := httptest.NewRequest(http.MethodGet, "/files", nil)
	document.Header.Set("Accept", "text/html")
	documentResponse := httptest.NewRecorder()
	handler.ServeHTTP(documentResponse, document)
	if documentResponse.Code != http.StatusSeeOther || documentResponse.Header().Get("Location") != "https://control.example.com/devices/device-1/open" {
		t.Fatalf("文档请求未跳转固定 Control 入口: %d %s", documentResponse.Code, documentResponse.Header().Get("Location"))
	}
	metaResponse := httptest.NewRecorder()
	handler.ServeHTTP(metaResponse, httptest.NewRequest(http.MethodGet, "/api/meta", nil))
	if metaResponse.Code != http.StatusOK || !strings.Contains(metaResponse.Body.String(), `"surface":"agent"`) {
		t.Fatalf("Agent 元数据必须公开 surface: %d %s", metaResponse.Code, metaResponse.Body.String())
	}

	apiRequest := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	apiResponse := httptest.NewRecorder()
	handler.ServeHTTP(apiResponse, apiRequest)
	if apiResponse.Code != http.StatusUnauthorized || !strings.Contains(apiResponse.Body.String(), "session_required") {
		t.Fatalf("未认证 API 应返回真实 401: %d %s", apiResponse.Code, apiResponse.Body.String())
	}
}

func TestAgentWriteOriginMustMatchConfiguredAgentURL(t *testing.T) {
	server := &Server{configStore: &ConfigStore{current: protocol.SignedDeviceConfig{Revision: 1, AgentURL: "https://nas.tail-name.ts.net:19443"}}}
	request := httptest.NewRequest(http.MethodPost, "/api/services/jellyfin/actions", nil)
	request.Header.Set("Origin", "https://evil.example")
	if server.validWriteOrigin(request) {
		t.Fatal("跨站 Origin 不应通过 Agent 写操作校验")
	}
	request.Header.Set("Origin", "https://nas.tail-name.ts.net:19443")
	if !server.validWriteOrigin(request) {
		t.Fatal("Agent 自身 Origin 应通过写操作校验")
	}
}
