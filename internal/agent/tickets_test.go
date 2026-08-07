package agent

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"
	"time"

	"github.com/wangshangbin/homestack/internal/protocol"
	"github.com/wangshangbin/homestack/internal/secure"
)

func TestAccessTicketCanOnlyBeRedeemedOnce(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	store, err := OpenSessionStore("", "device-1", "https://app.example.com", publicKey, "control-1")
	if err != nil {
		t.Fatal(err)
	}
	store.now = func() time.Time { return now }
	claims := protocol.AccessTicketClaims{
		Issuer: "https://app.example.com", Subject: "user-1", DeviceID: "device-1",
		Nonce: "nonce-1", IssuedAt: now, ExpiresAt: now.Add(30 * time.Second),
	}
	signed, err := secure.SignJWS(privateKey, "control-1", claims)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Redeem(signed); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Redeem(signed); !errors.Is(err, ErrTicketReplay) {
		t.Fatalf("期望票据重放错误，实际为 %v", err)
	}
}
