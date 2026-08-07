package control

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/github"
)

var ErrUnauthenticated = errors.New("身份验证失败")

type Identity struct {
	Subject         string `json:"subject"`
	Email           string `json:"email"`
	Name            string `json:"name"`
	Provider        string `json:"provider"`
	ProviderSubject string `json:"provider_subject"`
}

type ExternalIdentity struct {
	Provider      string
	Subject       string
	Email         string
	Name          string
	EmailVerified bool
}

type ProviderMetadata struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type Authenticator interface {
	Authenticate(context.Context, *http.Request) (Identity, error)
	Metadata() []ProviderMetadata
}

type WebAuthenticator interface {
	RegisterRoutes(*http.ServeMux)
}

type OAuthProvider struct {
	ID       string
	Label    string
	OAuth    oauth2.Config
	Verifier *oidc.IDTokenVerifier
	Kind     string
	Client   *http.Client
}

func NewOIDCProvider(ctx context.Context, id, label, issuer, clientID, clientSecret, publicURL string) (*OAuthProvider, error) {
	if id == "" || label == "" || issuer == "" || clientID == "" || publicURL == "" {
		return nil, errors.New("OIDC 提供商配置不完整")
	}
	provider, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, fmt.Errorf("加载 %s OIDC 发现文档失败: %w", label, err)
	}
	return &OAuthProvider{
		ID: id, Label: label, Kind: "oidc", Verifier: provider.Verifier(&oidc.Config{ClientID: clientID}),
		OAuth: oauth2.Config{ClientID: clientID, ClientSecret: clientSecret, Endpoint: provider.Endpoint(), RedirectURL: strings.TrimRight(publicURL, "/") + "/auth/callback/" + id, Scopes: []string{oidc.ScopeOpenID, "profile", "email"}},
	}, nil
}

func NewGitHubProvider(clientID, clientSecret, publicURL string) (*OAuthProvider, error) {
	if clientID == "" || clientSecret == "" || publicURL == "" {
		return nil, errors.New("GitHub OAuth 配置不完整")
	}
	return &OAuthProvider{
		ID: "github", Label: "GitHub", Kind: "github",
		OAuth: oauth2.Config{ClientID: clientID, ClientSecret: clientSecret, Endpoint: github.Endpoint, RedirectURL: strings.TrimRight(publicURL, "/") + "/auth/callback/github", Scopes: []string{"read:user", "user:email"}},
	}, nil
}

type pendingLogin struct {
	Provider        string
	Mode            string
	Verifier        string
	Nonce           string
	ReturnURL       string
	RedirectURI     string
	ClientState     string
	ClientChallenge string
	OwnerID         string
	ExpiresAt       time.Time
}

type appCode struct {
	OwnerID   string
	Challenge string
	ExpiresAt time.Time
}

type AuthManager struct {
	providers    map[string]*OAuthProvider
	store        *OwnerStore
	mu           sync.Mutex
	pending      map[string]pendingLogin
	appCodes     map[string]appCode
	reauthGrants map[string]time.Time
	now          func() time.Time
}

func NewAuthManager(providers []*OAuthProvider, store *OwnerStore) (*AuthManager, error) {
	if len(providers) == 0 || store == nil {
		return nil, errors.New("认证提供商和所有者存储必须配置")
	}
	manager := &AuthManager{providers: map[string]*OAuthProvider{}, store: store, pending: map[string]pendingLogin{}, appCodes: map[string]appCode{}, reauthGrants: map[string]time.Time{}, now: time.Now}
	for _, provider := range providers {
		if provider == nil || provider.ID == "" || manager.providers[provider.ID] != nil {
			return nil, errors.New("认证提供商 ID 无效或重复")
		}
		manager.providers[provider.ID] = provider
	}
	return manager, nil
}

func (a *AuthManager) Metadata() []ProviderMetadata {
	order := []string{"google", "github"}
	result := make([]ProviderMetadata, 0, len(a.providers))
	for _, id := range order {
		if provider := a.providers[id]; provider != nil {
			result = append(result, ProviderMetadata{ID: provider.ID, Label: provider.Label})
		}
	}
	return result
}

func (a *AuthManager) Authenticate(_ context.Context, request *http.Request) (Identity, error) {
	if cookie, err := request.Cookie("homestack_control_session"); err == nil {
		if owner, ok := a.store.ResolveSession(cookie.Value, "browser"); ok {
			return Identity{Subject: owner.ID, Email: owner.Email, Name: owner.Name}, nil
		}
	}
	parts := strings.Fields(request.Header.Get("Authorization"))
	if len(parts) == 2 && parts[0] == "Bearer" {
		if owner, ok := a.store.ResolveSession(parts[1], "access"); ok {
			return Identity{Subject: owner.ID, Email: owner.Email, Name: owner.Name}, nil
		}
	}
	return Identity{}, ErrUnauthenticated
}

func (a *AuthManager) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /auth/login/{provider}", a.startWebLogin)
	mux.HandleFunc("GET /auth/app/start/{provider}", a.startAppLogin)
	mux.HandleFunc("GET /auth/reauth/{provider}", a.startReauth)
	mux.HandleFunc("GET /auth/callback/{provider}", a.completeLogin)
	mux.HandleFunc("POST /api/auth/app/token", a.exchangeAppCode)
	mux.HandleFunc("POST /api/auth/app/refresh", a.refreshAppToken)
	mux.HandleFunc("POST /auth/logout", a.logout)
}

func (a *AuthManager) startReauth(writer http.ResponseWriter, request *http.Request) {
	identity, err := a.Authenticate(request.Context(), request)
	if err != nil {
		http.Error(writer, err.Error(), http.StatusUnauthorized)
		return
	}
	provider := a.providers[request.PathValue("provider")]
	if provider == nil || len(a.providers) != 1 {
		http.Error(writer, "重新认证必须使用当前登录方式", http.StatusBadRequest)
		return
	}
	returnURL := request.URL.Query().Get("return")
	if returnURL != "/identity" && returnURL != "/settings/domains" {
		returnURL = "/settings/domains"
	}
	a.start(writer, request, pendingLogin{Mode: "reauth", ReturnURL: returnURL, OwnerID: identity.Subject})
}

func (a *AuthManager) startWebLogin(writer http.ResponseWriter, request *http.Request) {
	returnURL := request.URL.Query().Get("return")
	if !validLocalReturnURL(returnURL) {
		returnURL = "/"
	}
	a.start(writer, request, pendingLogin{Mode: "web", ReturnURL: returnURL})
}

func (a *AuthManager) startAppLogin(writer http.ResponseWriter, request *http.Request) {
	redirectURI := request.URL.Query().Get("redirect_uri")
	clientState := request.URL.Query().Get("state")
	challenge := request.URL.Query().Get("code_challenge")
	if !validLoopbackRedirect(redirectURI) || clientState == "" || !validPKCEValue(challenge) {
		http.Error(writer, "App 登录参数无效", http.StatusBadRequest)
		return
	}
	a.start(writer, request, pendingLogin{Mode: "app", RedirectURI: redirectURI, ClientState: clientState, ClientChallenge: challenge})
}

func (a *AuthManager) start(writer http.ResponseWriter, request *http.Request, pending pendingLogin) {
	provider := a.providers[request.PathValue("provider")]
	if provider == nil {
		http.Error(writer, "不支持的登录方式", http.StatusNotFound)
		return
	}
	state, err := randomStoreToken(32)
	if err != nil {
		http.Error(writer, err.Error(), http.StatusInternalServerError)
		return
	}
	verifier, err := randomStoreToken(64)
	if err != nil {
		http.Error(writer, err.Error(), http.StatusInternalServerError)
		return
	}
	nonce, err := randomStoreToken(32)
	if err != nil {
		http.Error(writer, err.Error(), http.StatusInternalServerError)
		return
	}
	pending.Provider = provider.ID
	pending.Verifier = verifier
	pending.Nonce = nonce
	pending.ExpiresAt = a.now().UTC().Add(5 * time.Minute)
	a.mu.Lock()
	a.pruneLocked()
	a.pending[state] = pending
	a.mu.Unlock()
	digest := sha256.Sum256([]byte(verifier))
	options := []oauth2.AuthCodeOption{
		oauth2.SetAuthURLParam("code_challenge", base64.RawURLEncoding.EncodeToString(digest[:])),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	}
	if pending.Mode == "reauth" {
		options = append(options, oauth2.SetAuthURLParam("prompt", "login"), oauth2.SetAuthURLParam("max_age", "0"))
	}
	if provider.Kind == "oidc" {
		options = append(options, oidc.Nonce(nonce))
	}
	http.Redirect(writer, request, provider.OAuth.AuthCodeURL(state, options...), http.StatusFound)
}

func (a *AuthManager) completeLogin(writer http.ResponseWriter, request *http.Request) {
	state := request.URL.Query().Get("state")
	a.mu.Lock()
	pending, ok := a.pending[state]
	delete(a.pending, state)
	a.mu.Unlock()
	if !ok || !a.now().UTC().Before(pending.ExpiresAt) || pending.Provider != request.PathValue("provider") {
		http.Error(writer, "登录状态无效或已过期", http.StatusBadRequest)
		return
	}
	if message := request.URL.Query().Get("error"); message != "" {
		http.Error(writer, "登录提供商拒绝授权: "+message, http.StatusUnauthorized)
		return
	}
	provider := a.providers[pending.Provider]
	token, err := provider.OAuth.Exchange(request.Context(), request.URL.Query().Get("code"), oauth2.SetAuthURLParam("code_verifier", pending.Verifier))
	if err != nil {
		http.Error(writer, "兑换 OAuth 授权码失败: "+err.Error(), http.StatusBadGateway)
		return
	}
	external, err := provider.externalIdentity(request.Context(), token, pending.Nonce)
	if err != nil {
		http.Error(writer, err.Error(), http.StatusUnauthorized)
		return
	}
	if pending.Mode == "reauth" {
		owner, exists := a.store.Owner()
		key := IdentityKey{Provider: external.Provider, Subject: external.Subject}
		if !exists || owner.ID != pending.OwnerID || !containsIdentity(owner.Identities, key) {
			http.Error(writer, "重新认证身份不是当前 Owner", http.StatusForbidden)
			return
		}
		cookie, err := request.Cookie("homestack_control_session")
		if err != nil {
			http.Error(writer, "重新认证缺少浏览器会话", http.StatusUnauthorized)
			return
		}
		a.mu.Lock()
		a.reauthGrants[owner.ID+"\x00"+hashAuthToken(cookie.Value)] = a.now().UTC().Add(5 * time.Minute)
		a.mu.Unlock()
		http.Redirect(writer, request, pending.ReturnURL+"?reauthenticated=1", http.StatusSeeOther)
		return
	}
	identity, err := a.store.AuthenticateOrClaim(external)
	if err != nil {
		http.Error(writer, err.Error(), http.StatusForbidden)
		return
	}
	if pending.Mode == "app" {
		a.completeAppLogin(writer, request, pending, identity)
		return
	}
	raw, expiresAt, err := a.store.CreateSession(identity.Subject, "browser", 8*time.Hour)
	if err != nil {
		http.Error(writer, err.Error(), http.StatusInternalServerError)
		return
	}
	http.SetCookie(writer, &http.Cookie{Name: "homestack_control_session", Value: raw, Path: "/", Expires: expiresAt, Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	http.Redirect(writer, request, pending.ReturnURL, http.StatusSeeOther)
}

func (a *AuthManager) ConsumeReauth(request *http.Request, ownerID string) bool {
	cookie, err := request.Cookie("homestack_control_session")
	if err != nil {
		return false
	}
	key := ownerID + "\x00" + hashAuthToken(cookie.Value)
	a.mu.Lock()
	defer a.mu.Unlock()
	expiresAt, ok := a.reauthGrants[key]
	delete(a.reauthGrants, key)
	return ok && a.now().UTC().Before(expiresAt)
}

func (a *AuthManager) completeAppLogin(writer http.ResponseWriter, request *http.Request, pending pendingLogin, identity Identity) {
	code, err := randomStoreToken(32)
	if err != nil {
		http.Error(writer, err.Error(), http.StatusInternalServerError)
		return
	}
	a.mu.Lock()
	a.appCodes[hashAuthToken(code)] = appCode{OwnerID: identity.Subject, Challenge: pending.ClientChallenge, ExpiresAt: a.now().UTC().Add(2 * time.Minute)}
	a.mu.Unlock()
	target, _ := url.Parse(pending.RedirectURI)
	query := target.Query()
	query.Set("code", code)
	query.Set("state", pending.ClientState)
	target.RawQuery = query.Encode()
	http.Redirect(writer, request, target.String(), http.StatusSeeOther)
}

func (a *AuthManager) exchangeAppCode(writer http.ResponseWriter, request *http.Request) {
	var body struct {
		Code     string `json:"code"`
		Verifier string `json:"code_verifier"`
	}
	if err := decodeJSONBody(writer, request, &body); err != nil {
		writeControlError(writer, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	a.mu.Lock()
	entry, ok := a.appCodes[hashAuthToken(body.Code)]
	delete(a.appCodes, hashAuthToken(body.Code))
	a.mu.Unlock()
	digest := sha256.Sum256([]byte(body.Verifier))
	actual := base64.RawURLEncoding.EncodeToString(digest[:])
	if !ok || !a.now().UTC().Before(entry.ExpiresAt) || subtle.ConstantTimeCompare([]byte(actual), []byte(entry.Challenge)) != 1 {
		writeControlError(writer, http.StatusUnauthorized, "code_rejected", "App 授权码无效、已使用或已过期")
		return
	}
	a.writeAppTokens(writer, entry.OwnerID)
}

func (a *AuthManager) refreshAppToken(writer http.ResponseWriter, request *http.Request) {
	var body struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := decodeJSONBody(writer, request, &body); err != nil {
		writeControlError(writer, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	owner, ok := a.store.ResolveSession(body.RefreshToken, "refresh")
	if !ok {
		writeControlError(writer, http.StatusUnauthorized, "refresh_rejected", "App 刷新令牌无效或已过期")
		return
	}
	if err := a.store.RevokeSession(body.RefreshToken); err != nil {
		writeControlError(writer, http.StatusInternalServerError, "refresh_failed", err.Error())
		return
	}
	a.writeAppTokens(writer, owner.ID)
}

func (a *AuthManager) writeAppTokens(writer http.ResponseWriter, ownerID string) {
	tokens, err := a.IssueAppTokens(ownerID)
	if err != nil {
		writeControlError(writer, http.StatusInternalServerError, "token_failed", err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, tokens)
}

type AppTokens struct {
	AccessToken      string    `json:"access_token"`
	ExpiresAt        time.Time `json:"expires_at"`
	RefreshToken     string    `json:"refresh_token"`
	RefreshExpiresAt time.Time `json:"refresh_expires_at"`
	TokenType        string    `json:"token_type"`
}

func (a *AuthManager) IssueAppTokens(ownerID string) (AppTokens, error) {
	access, accessExpiry, err := a.store.CreateSession(ownerID, "access", time.Hour)
	if err != nil {
		return AppTokens{}, err
	}
	refresh, refreshExpiry, err := a.store.CreateSession(ownerID, "refresh", 30*24*time.Hour)
	if err != nil {
		_ = a.store.RevokeSession(access)
		return AppTokens{}, err
	}
	return AppTokens{AccessToken: access, ExpiresAt: accessExpiry, RefreshToken: refresh, RefreshExpiresAt: refreshExpiry, TokenType: "Bearer"}, nil
}

func (a *AuthManager) logout(writer http.ResponseWriter, request *http.Request) {
	if cookie, err := request.Cookie("homestack_control_session"); err == nil {
		_ = a.store.RevokeSession(cookie.Value)
	}
	http.SetCookie(writer, &http.Cookie{Name: "homestack_control_session", Value: "", Path: "/", MaxAge: -1, Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	writer.WriteHeader(http.StatusNoContent)
}

func (p *OAuthProvider) externalIdentity(ctx context.Context, token *oauth2.Token, expectedNonce string) (ExternalIdentity, error) {
	if p.Kind == "github" {
		return p.githubIdentity(ctx, token)
	}
	raw, ok := token.Extra("id_token").(string)
	if !ok || raw == "" {
		return ExternalIdentity{}, fmt.Errorf("%s 未返回 ID Token", p.Label)
	}
	idToken, err := p.Verifier.Verify(ctx, raw)
	if err != nil {
		return ExternalIdentity{}, fmt.Errorf("验证 %s ID Token 失败: %w", p.Label, err)
	}
	var claims struct {
		Subject       string `json:"sub"`
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		Name          string `json:"name"`
		Nonce         string `json:"nonce"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return ExternalIdentity{}, fmt.Errorf("读取 %s 身份声明失败: %w", p.Label, err)
	}
	if claims.Nonce != expectedNonce {
		return ExternalIdentity{}, errors.New("OIDC nonce 验证失败")
	}
	return ExternalIdentity{Provider: p.ID, Subject: claims.Subject, Email: claims.Email, Name: claims.Name, EmailVerified: claims.EmailVerified}, nil
}

func (p *OAuthProvider) githubIdentity(ctx context.Context, token *oauth2.Token) (ExternalIdentity, error) {
	client := p.OAuth.Client(ctx, token)
	var user struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
		Name  string `json:"name"`
		Email string `json:"email"`
	}
	if err := getOAuthJSON(client, "https://api.github.com/user", &user); err != nil {
		return ExternalIdentity{}, fmt.Errorf("读取 GitHub 用户信息失败: %w", err)
	}
	var emails []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	if err := getOAuthJSON(client, "https://api.github.com/user/emails", &emails); err != nil {
		return ExternalIdentity{}, fmt.Errorf("读取 GitHub 已验证邮箱失败: %w", err)
	}
	verifiedEmail := ""
	for _, value := range emails {
		if value.Primary && value.Verified {
			verifiedEmail = value.Email
			break
		}
	}
	if user.ID <= 0 || verifiedEmail == "" {
		return ExternalIdentity{}, errors.New("GitHub 身份必须包含稳定用户 ID 和已验证主邮箱")
	}
	name := user.Name
	if name == "" {
		name = user.Login
	}
	return ExternalIdentity{Provider: p.ID, Subject: fmt.Sprintf("%d", user.ID), Email: verifiedEmail, Name: name, EmailVerified: true}, nil
}

func getOAuthJSON(client *http.Client, endpoint string, target any) error {
	request, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", response.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(target)
}

func validLocalReturnURL(raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil && !parsed.IsAbs() && strings.HasPrefix(parsed.Path, "/") && parsed.Host == ""
}

func validLoopbackRedirect(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "http" || parsed.User != nil || parsed.Port() == "" || parsed.Path != "/callback" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	host := parsed.Hostname()
	return host == "127.0.0.1" || host == "::1"
}

func validPKCEValue(raw string) bool {
	if len(raw) < 43 || len(raw) > 128 {
		return false
	}
	_, err := base64.RawURLEncoding.DecodeString(raw)
	return err == nil
}

func (a *AuthManager) pruneLocked() {
	now := a.now().UTC()
	for key, value := range a.pending {
		if !now.Before(value.ExpiresAt) {
			delete(a.pending, key)
		}
	}
	for key, value := range a.appCodes {
		if !now.Before(value.ExpiresAt) {
			delete(a.appCodes, key)
		}
	}
	for key, expiresAt := range a.reauthGrants {
		if !now.Before(expiresAt) {
			delete(a.reauthGrants, key)
		}
	}
}
