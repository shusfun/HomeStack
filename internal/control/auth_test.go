package control

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func TestOIDCLoginStartUsesStateNonceAndS256PKCE(t *testing.T) {
	store, err := OpenOwnerStore("")
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewAuthManager([]*OAuthProvider{{
		ID: "google", Label: "Google", Kind: "oidc",
		OAuth: oauth2.Config{ClientID: "client", RedirectURL: "https://control.example.com/auth/callback/google", Endpoint: oauth2.Endpoint{AuthURL: "https://accounts.example.com/authorize"}},
	}}, store)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	manager.RegisterRoutes(mux)
	request := httptest.NewRequest(http.MethodGet, "/auth/login/google?return=/devices/device-1/open", nil)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusFound {
		t.Fatalf("OIDC 登录发起失败: %d %s", response.Code, response.Body.String())
	}
	target, err := url.Parse(response.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	query := target.Query()
	state := query.Get("state")
	if state == "" || query.Get("nonce") == "" || query.Get("code_challenge_method") != "S256" || !validPKCEValue(query.Get("code_challenge")) {
		t.Fatalf("OIDC 登录缺少 state、nonce 或 S256 PKCE: %s", target.String())
	}
	manager.mu.Lock()
	pending, ok := manager.pending[state]
	manager.mu.Unlock()
	if !ok || pending.Provider != "google" || pending.Nonce != query.Get("nonce") || pending.ReturnURL != "/devices/device-1/open" {
		t.Fatalf("服务端登录状态未绑定提供商、nonce 或固定回跳: %+v", pending)
	}
}

func TestLoopbackRedirectValidation(t *testing.T) {
	valid := []string{"http://127.0.0.1:43123/callback", "http://[::1]:43123/callback"}
	invalid := []string{"https://127.0.0.1:43123/callback", "http://localhost:43123/callback", "http://127.0.0.1/callback", "http://127.0.0.1:43123/other", "http://127.0.0.1:43123/callback?x=1"}
	for _, value := range valid {
		if !validLoopbackRedirect(value) {
			t.Errorf("合法回环地址被拒绝: %s", value)
		}
	}
	for _, value := range invalid {
		if validLoopbackRedirect(value) {
			t.Errorf("非固定回环地址不应通过: %s", value)
		}
	}
}

func TestAppCodeIsSingleUseAndPKCEBound(t *testing.T) {
	now := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	store, _ := OpenOwnerStore("")
	store.now = func() time.Time { return now }
	identity, err := store.AuthenticateOrClaim(ExternalIdentity{Provider: "pocket", Subject: "owner", Email: "owner@example.com", EmailVerified: true})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewAuthManager([]*OAuthProvider{{ID: "github", Label: "GitHub"}, {ID: "pocket", Label: "Pocket ID"}, {ID: "google", Label: "Google"}}, store)
	if err != nil {
		t.Fatal(err)
	}
	manager.now = func() time.Time { return now }
	verifier := strings.Repeat("a", 64)
	digest := sha256.Sum256([]byte(verifier))
	code := "single-use-code"
	manager.appCodes[hashAuthToken(code)] = appCode{OwnerID: identity.Subject, Challenge: base64.RawURLEncoding.EncodeToString(digest[:]), ExpiresAt: now.Add(time.Minute)}

	exchange := func(value string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/app/token", strings.NewReader(`{"code":"`+code+`","code_verifier":"`+value+`"}`))
		response := httptest.NewRecorder()
		manager.exchangeAppCode(response, request)
		return response
	}
	first := exchange(verifier)
	if first.Code != http.StatusOK {
		t.Fatalf("首次兑换失败: %d %s", first.Code, first.Body.String())
	}
	var tokens map[string]any
	if err := json.Unmarshal(first.Body.Bytes(), &tokens); err != nil || tokens["access_token"] == "" || tokens["refresh_token"] == "" {
		t.Fatalf("App token 响应无效: %s err=%v", first.Body.String(), err)
	}
	second := exchange(verifier)
	if second.Code != http.StatusUnauthorized {
		t.Fatalf("重放 App code 应返回 401，实际 %d", second.Code)
	}
}

func TestProviderMetadataUsesStableOrder(t *testing.T) {
	store, _ := OpenOwnerStore("")
	manager, err := NewAuthManager([]*OAuthProvider{{ID: "github", Label: "GitHub"}, {ID: "google", Label: "Google"}, {ID: "pocket", Label: "Pocket ID"}}, store)
	if err != nil {
		t.Fatal(err)
	}
	metadata := manager.Metadata()
	if len(metadata) != 3 || metadata[0].ID != "pocket" || metadata[1].ID != "google" || metadata[2].ID != "github" {
		t.Fatalf("登录方式顺序不稳定: %+v", metadata)
	}
}
