package invite

import (
	"bytes"
	"errors"
	"testing"
	"time"
)

func TestInviteCanOnlyBeRedeemedOnce(t *testing.T) {
	now := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	store := NewMemory(func() time.Time { return now }, bytes.NewReader(make([]byte, 64)))
	descriptor, _, err := store.Create("https://app.example.com:8443", "admin", 10*time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Redeem(descriptor.Code); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Redeem(descriptor.Code); !errors.Is(err, ErrUsed) {
		t.Fatalf("期望重复使用错误，实际为 %v", err)
	}
}

func TestExpiredInviteIsRejected(t *testing.T) {
	now := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	store := NewMemory(func() time.Time { return now }, bytes.NewReader(make([]byte, 64)))
	descriptor, _, err := store.Create("https://app.example.com:8443", "admin", 10*time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(10 * time.Minute)
	if _, err := store.Redeem(descriptor.Code); !errors.Is(err, ErrExpired) {
		t.Fatalf("期望过期错误，实际为 %v", err)
	}
}
