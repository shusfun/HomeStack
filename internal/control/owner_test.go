package control

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOwnerStoreClaimsFirstIdentityAndReplacesProviderExplicitly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "owner.json")
	store, err := OpenOwnerStore(path)
	if err != nil {
		t.Fatal(err)
	}
	first := ExternalIdentity{Provider: "github", Subject: "owner-1", Email: "owner@example.com", Name: "Owner", EmailVerified: true}
	identity, err := store.AuthenticateOrClaim(first)
	if err != nil || identity.Subject == "" {
		t.Fatalf("首个身份认领失败: identity=%+v err=%v", identity, err)
	}
	sameEmail := ExternalIdentity{Provider: "google", Subject: "google-1", Email: first.Email, Name: first.Name, EmailVerified: true}
	if _, err := store.AuthenticateOrClaim(sameEmail); !errors.Is(err, ErrIdentityNotLinked) {
		t.Fatalf("同邮箱未绑定身份必须被拒绝，实际错误: %v", err)
	}
	previous, err := store.ReplaceIdentity(identity.Subject, sameEmail)
	if err != nil {
		t.Fatal(err)
	}
	linked, err := store.AuthenticateOrClaim(sameEmail)
	if err != nil || linked.Subject != identity.Subject {
		t.Fatalf("显式替换后登录失败: identity=%+v err=%v", linked, err)
	}
	if err := store.RestoreOwner(previous); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AuthenticateOrClaim(first); err != nil {
		t.Fatalf("恢复旧身份失败: %v", err)
	}
}

func TestOwnerStorePersistsOnlyHashedSessionAndEnforcesKindAndExpiry(t *testing.T) {
	now := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "owner.json")
	store, err := OpenOwnerStore(path)
	if err != nil {
		t.Fatal(err)
	}
	store.now = func() time.Time { return now }
	identity, err := store.AuthenticateOrClaim(ExternalIdentity{Provider: "github", Subject: "42", Email: "owner@example.com", EmailVerified: true})
	if err != nil {
		t.Fatal(err)
	}
	raw, _, err := store.CreateSession(identity.Subject, "access", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), raw) {
		t.Fatal("持久化状态不允许包含原始会话令牌")
	}
	if _, ok := store.ResolveSession(raw, "refresh"); ok {
		t.Fatal("access token 不得作为 refresh token 使用")
	}
	if _, ok := store.ResolveSession(raw, "access"); !ok {
		t.Fatal("有效 access token 应通过验证")
	}
	now = now.Add(2 * time.Minute)
	if _, ok := store.ResolveSession(raw, "access"); ok {
		t.Fatal("过期 access token 不得通过验证")
	}
}
