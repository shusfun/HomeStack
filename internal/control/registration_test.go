package control

import (
	"bytes"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/wangshangbin/homestack/internal/protocol"
)

func TestRegistrationLocksTailnetAndRotatesDuplicateCredential(t *testing.T) {
	_, signingKey, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	store := NewMemoryDeviceStore()
	service, err := NewRegistrationService(store, signingKey, "control", "https://home.example.com", func() time.Time { return now }, bytes.NewReader(make([]byte, 4096)))
	if err != nil {
		t.Fatal(err)
	}
	request := validNodeRegistration(t, "mac.tail-name.ts.net")
	first, err := service.Register("owner", request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Register("owner", request)
	if err != nil {
		t.Fatal(err)
	}
	if first.DeviceID != second.DeviceID {
		t.Fatal("同一设备公钥被重复登记")
	}
	record, _ := store.Owned(first.DeviceID, "owner")
	if record.Config.Revision != 2 {
		t.Fatalf("重复登记未递增配置 revision: %d", record.Config.Revision)
	}
	other := validNodeRegistration(t, "linux.other-tail.ts.net")
	if _, err := service.Register("owner", other); !errors.Is(err, ErrTailnetChanged) {
		t.Fatalf("不同 Tailnet 未被拒绝: %v", err)
	}
	if err := store.Remove(first.DeviceID, "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Register("owner", other); err != nil {
		t.Fatalf("移除全部设备后仍不能更换 Tailnet: %v", err)
	}
}

func TestActivationCodeExpiresAndCannotReplay(t *testing.T) {
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	store, _ := OpenActivationStore("", func() time.Time { return now }, bytes.NewReader(make([]byte, 128)))
	code, _, err := store.Create("owner")
	if err != nil {
		t.Fatal(err)
	}
	if owner, err := store.Redeem(code); err != nil || owner != "owner" {
		t.Fatalf("兑换失败: %s %v", owner, err)
	}
	if _, err := store.Redeem(code); !errors.Is(err, ErrActivationRejected) {
		t.Fatalf("激活码可重放: %v", err)
	}
	code, _, _ = store.Create("owner")
	now = now.Add(10 * time.Minute)
	if _, err := store.Redeem(code); !errors.Is(err, ErrActivationRejected) {
		t.Fatalf("过期激活码仍可用: %v", err)
	}
}

func validNodeRegistration(t *testing.T, dns string) protocol.NodeRegistration {
	t.Helper()
	_, identityKey, _ := ed25519.GenerateKey(rand.Reader)
	encryptionKey, _ := ecdh.X25519().GenerateKey(rand.Reader)
	return protocol.NodeRegistration{Name: "device", Platform: "linux", Architecture: "amd64", TailscaleIP: "100.64.0.8", MagicDNS: dns, DevicePublicKey: base64.RawURLEncoding.EncodeToString(identityKey.Public().(ed25519.PublicKey)), EncryptionPublicKey: base64.RawURLEncoding.EncodeToString(encryptionKey.PublicKey().Bytes())}
}
