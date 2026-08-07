package control

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func TestAuthManagerExposesOnlyConfiguredProvider(t *testing.T) {
	store, err := OpenOwnerStore("")
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewAuthManager([]*OAuthProvider{{ID: "github", Label: "GitHub"}}, store)
	if err != nil {
		t.Fatal(err)
	}
	metadata := manager.Metadata()
	if len(metadata) != 1 || metadata[0].ID != "github" {
		t.Fatalf("登录方式不唯一: %+v", metadata)
	}
}

func TestAppAuthorizationCodeUsesPKCEAndSingleUse(t *testing.T) {
	store, _ := OpenOwnerStore("")
	owner, _ := store.AuthenticateOrClaim(ExternalIdentity{Provider: "github", Subject: "owner", Email: "owner@example.com", EmailVerified: true})
	manager, _ := NewAuthManager([]*OAuthProvider{{ID: "github", Label: "GitHub", OAuth: oauth2.Config{ClientID: "client", Endpoint: oauth2.Endpoint{AuthURL: "https://github.example/authorize"}}}}, store)
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	manager.now = func() time.Time { return now }
	verifier := strings.Repeat("a", 64)
	digest := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(digest[:])
	manager.appCodes[hashAuthToken("code")] = appCode{OwnerID: owner.Subject, Challenge: challenge, ExpiresAt: now.Add(time.Minute)}
	request := httptest.NewRequest(http.MethodPost, "/api/auth/app/token", strings.NewReader(`{"code":"code","code_verifier":"`+verifier+`"}`))
	response := httptest.NewRecorder()
	manager.exchangeAppCode(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("兑换失败: %d %s", response.Code, response.Body.String())
	}
	replay := httptest.NewRecorder()
	manager.exchangeAppCode(replay, httptest.NewRequest(http.MethodPost, "/api/auth/app/token", strings.NewReader(`{"code":"code","code_verifier":"`+verifier+`"}`)))
	if replay.Code != http.StatusUnauthorized {
		t.Fatalf("授权码可重放: %d", replay.Code)
	}
}

func TestReauthGrantIsBoundToOwnerAndBrowserSessionAndSingleUse(t *testing.T) {
	store, _ := OpenOwnerStore("")
	manager, _ := NewAuthManager([]*OAuthProvider{{ID: "github", Label: "GitHub"}}, store)
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	manager.now = func() time.Time { return now }
	request := httptest.NewRequest(http.MethodPost, "/api/system/reconfigure", nil)
	request.AddCookie(&http.Cookie{Name: "homestack_control_session", Value: "browser-session"})
	key := "owner\x00" + hashAuthToken("browser-session")
	manager.reauthGrants[key] = now.Add(5 * time.Minute)

	if manager.ConsumeReauth(request, "other-owner") {
		t.Fatal("重新认证授权可被其他 Owner 使用")
	}
	if !manager.ConsumeReauth(request, "owner") {
		t.Fatal("有效的重新认证授权未被接受")
	}
	if manager.ConsumeReauth(request, "owner") {
		t.Fatal("重新认证授权可重复使用")
	}

	manager.reauthGrants[key] = now
	if manager.ConsumeReauth(request, "owner") {
		t.Fatal("过期的重新认证授权仍然有效")
	}
}
