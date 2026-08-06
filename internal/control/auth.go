package control

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

var ErrUnauthenticated = errors.New("身份验证失败")

type Identity struct {
	Subject string
	Email   string
	Name    string
	Groups  []string
	Admin   bool
}

type OIDCMetadata struct {
	Issuer                string `json:"issuer"`
	ClientID              string `json:"client_id"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
}

type Authenticator interface {
	Authenticate(ctx context.Context, request *http.Request) (Identity, error)
	Metadata() OIDCMetadata
}

type WebAuthenticator interface {
	StartWebLogin(http.ResponseWriter, *http.Request)
	CompleteWebLogin(http.ResponseWriter, *http.Request)
	Logout(http.ResponseWriter, *http.Request)
}

type OIDCAuthenticator struct {
	verifier   *oidc.IDTokenVerifier
	metadata   OIDCMetadata
	adminGroup string
	oauth      oauth2.Config
	mu         sync.Mutex
	pending    map[string]pendingLogin
	sessions   map[string]webSession
}

type pendingLogin struct {
	Verifier  string
	Nonce     string
	ReturnURL string
	ExpiresAt time.Time
}

type webSession struct {
	Identity  Identity
	ExpiresAt time.Time
}

func NewOIDCAuthenticator(ctx context.Context, issuer, clientID, adminGroup, publicURL string) (*OIDCAuthenticator, error) {
	if issuer == "" || clientID == "" || adminGroup == "" || publicURL == "" {
		return nil, errors.New("OIDC issuer、client_id、管理员组和公网地址必须明确配置")
	}
	provider, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, fmt.Errorf("加载 OIDC 发现文档失败: %w", err)
	}
	endpoint := provider.Endpoint()
	authenticator := &OIDCAuthenticator{
		verifier: provider.Verifier(&oidc.Config{ClientID: clientID}),
		metadata: OIDCMetadata{
			Issuer: issuer, ClientID: clientID, AuthorizationEndpoint: endpoint.AuthURL, TokenEndpoint: endpoint.TokenURL,
		},
		adminGroup: adminGroup,
		oauth: oauth2.Config{
			ClientID: clientID, Endpoint: endpoint, RedirectURL: strings.TrimRight(publicURL, "/") + "/auth/callback",
			Scopes: []string{oidc.ScopeOpenID, "profile", "email", "groups"},
		},
		pending: map[string]pendingLogin{}, sessions: map[string]webSession{},
	}
	return authenticator, nil
}

func (a *OIDCAuthenticator) Authenticate(ctx context.Context, request *http.Request) (Identity, error) {
	if cookie, err := request.Cookie("homestack_control_session"); err == nil {
		a.mu.Lock()
		session, exists := a.sessions[cookie.Value]
		if exists && time.Now().UTC().Before(session.ExpiresAt) {
			a.mu.Unlock()
			return session.Identity, nil
		}
		delete(a.sessions, cookie.Value)
		a.mu.Unlock()
	}
	authorization := request.Header.Get("Authorization")
	parts := strings.Fields(authorization)
	if len(parts) != 2 || parts[0] != "Bearer" {
		return Identity{}, ErrUnauthenticated
	}
	token, err := a.verifier.Verify(ctx, parts[1])
	if err != nil {
		return Identity{}, fmt.Errorf("OIDC ID Token 验证失败: %w", err)
	}
	return a.identityFromToken(token, "")
}

func (a *OIDCAuthenticator) StartWebLogin(writer http.ResponseWriter, request *http.Request) {
	state, err := secureRandomToken(32)
	if err != nil {
		http.Error(writer, err.Error(), http.StatusInternalServerError)
		return
	}
	nonce, err := secureRandomToken(32)
	if err != nil {
		http.Error(writer, err.Error(), http.StatusInternalServerError)
		return
	}
	verifier, err := secureRandomToken(64)
	if err != nil {
		http.Error(writer, err.Error(), http.StatusInternalServerError)
		return
	}
	returnURL := request.URL.Query().Get("return")
	if !validLocalReturnURL(returnURL) {
		returnURL = "/"
	}
	now := time.Now().UTC()
	a.mu.Lock()
	for key, pending := range a.pending {
		if !now.Before(pending.ExpiresAt) {
			delete(a.pending, key)
		}
	}
	a.pending[state] = pendingLogin{Verifier: verifier, Nonce: nonce, ReturnURL: returnURL, ExpiresAt: now.Add(5 * time.Minute)}
	a.mu.Unlock()
	challengeBytes := sha256.Sum256([]byte(verifier))
	authURL := a.oauth.AuthCodeURL(
		state, oidc.Nonce(nonce),
		oauth2.SetAuthURLParam("code_challenge", base64.RawURLEncoding.EncodeToString(challengeBytes[:])),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	)
	http.Redirect(writer, request, authURL, http.StatusFound)
}

func (a *OIDCAuthenticator) CompleteWebLogin(writer http.ResponseWriter, request *http.Request) {
	state := request.URL.Query().Get("state")
	a.mu.Lock()
	pending, exists := a.pending[state]
	delete(a.pending, state)
	a.mu.Unlock()
	if !exists || !time.Now().UTC().Before(pending.ExpiresAt) {
		http.Error(writer, "OIDC 登录状态无效或已过期", http.StatusBadRequest)
		return
	}
	if oidcError := request.URL.Query().Get("error"); oidcError != "" {
		http.Error(writer, "Pocket ID 拒绝登录: "+oidcError, http.StatusUnauthorized)
		return
	}
	token, err := a.oauth.Exchange(request.Context(), request.URL.Query().Get("code"), oauth2.SetAuthURLParam("code_verifier", pending.Verifier))
	if err != nil {
		http.Error(writer, "兑换 OIDC 授权码失败: "+err.Error(), http.StatusBadGateway)
		return
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		http.Error(writer, "Pocket ID 未返回 ID Token", http.StatusBadGateway)
		return
	}
	idToken, err := a.verifier.Verify(request.Context(), rawIDToken)
	if err != nil {
		http.Error(writer, "OIDC ID Token 验证失败: "+err.Error(), http.StatusUnauthorized)
		return
	}
	identity, err := a.identityFromToken(idToken, pending.Nonce)
	if err != nil {
		http.Error(writer, err.Error(), http.StatusUnauthorized)
		return
	}
	sessionToken, err := secureRandomToken(32)
	if err != nil {
		http.Error(writer, err.Error(), http.StatusInternalServerError)
		return
	}
	expiresAt := time.Now().UTC().Add(8 * time.Hour)
	if idToken.Expiry.Before(expiresAt) {
		expiresAt = idToken.Expiry
	}
	a.mu.Lock()
	a.sessions[sessionToken] = webSession{Identity: identity, ExpiresAt: expiresAt}
	a.mu.Unlock()
	http.SetCookie(writer, &http.Cookie{
		Name: "homestack_control_session", Value: sessionToken, Path: "/", Expires: expiresAt,
		Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode,
	})
	http.Redirect(writer, request, pending.ReturnURL, http.StatusSeeOther)
}

func (a *OIDCAuthenticator) Logout(writer http.ResponseWriter, request *http.Request) {
	if cookie, err := request.Cookie("homestack_control_session"); err == nil {
		a.mu.Lock()
		delete(a.sessions, cookie.Value)
		a.mu.Unlock()
	}
	http.SetCookie(writer, &http.Cookie{
		Name: "homestack_control_session", Value: "", Path: "/", MaxAge: -1,
		Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode,
	})
	writer.WriteHeader(http.StatusNoContent)
}

func (a *OIDCAuthenticator) identityFromToken(token *oidc.IDToken, expectedNonce string) (Identity, error) {
	var claims struct {
		Subject       string   `json:"sub"`
		Email         string   `json:"email"`
		EmailVerified bool     `json:"email_verified"`
		Name          string   `json:"name"`
		Groups        []string `json:"groups"`
		Nonce         string   `json:"nonce"`
	}
	if err := token.Claims(&claims); err != nil {
		return Identity{}, fmt.Errorf("读取 OIDC 身份声明失败: %w", err)
	}
	if expectedNonce != "" && claims.Nonce != expectedNonce {
		return Identity{}, errors.New("OIDC nonce 验证失败")
	}
	if claims.Subject == "" || claims.Email == "" || !claims.EmailVerified {
		return Identity{}, errors.New("OIDC 身份必须包含 sub 和已验证的 email")
	}
	return Identity{
		Subject: claims.Subject, Email: claims.Email, Name: claims.Name, Groups: claims.Groups,
		Admin: slices.Contains(claims.Groups, a.adminGroup),
	}, nil
}

func validLocalReturnURL(raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil && parsed.IsAbs() == false && strings.HasPrefix(parsed.Path, "/") && parsed.Host == ""
}

func secureRandomToken(size int) (string, error) {
	data := make([]byte, size)
	if _, err := io.ReadFull(rand.Reader, data); err != nil {
		return "", fmt.Errorf("生成 OIDC 安全随机数失败: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func (a *OIDCAuthenticator) Metadata() OIDCMetadata {
	return a.metadata
}
