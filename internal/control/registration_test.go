package control

import (
	"bytes"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"reflect"
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

func TestRegistrationPreservesManagedContentConfiguration(t *testing.T) {
	_, signingKey, _ := ed25519.GenerateKey(rand.Reader)
	store := NewMemoryDeviceStore()
	service, err := NewRegistrationService(store, signingKey, "control", "https://home.example.com", time.Now, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	request := validNodeRegistration(t, "mac.tail-name.ts.net")
	request.Platform = "darwin"
	request.Modules = []protocol.ModuleConfig{
		{ID: "filebrowser", Enabled: true, BaseURL: "http://127.0.0.1:19445", ReadOnly: true},
		{ID: "jellyfin", Enabled: true, BaseURL: "http://127.0.0.1:19446", ReadOnly: true},
	}
	request.SharedDirectories = []protocol.SharedDirectory{{ID: "downloads", Name: "下载", Path: "/Users/test/Downloads"}}
	response, err := service.Register("owner", request)
	if err != nil {
		t.Fatal(err)
	}
	record, err := store.Owned(response.DeviceID, "owner")
	if err != nil {
		t.Fatalf("读取登记后的设备失败: %v", err)
	}
	if !reflect.DeepEqual(record.Config.Modules, request.Modules) || !reflect.DeepEqual(record.Config.SharedDirectories, request.SharedDirectories) {
		t.Fatalf("Control 未保留托管内容配置: %+v", record.Config)
	}
}

func TestValidateNodeRegistrationAcceptsCanonicalWindowsSharedDirectories(t *testing.T) {
	for name, directory := range map[string]protocol.SharedDirectory{
		"盘符路径":   {ID: "downloads", Name: "下载", Path: `C:\Users\test\Downloads`},
		"UNC 路径": {ID: "media", Name: "媒体", Path: `\\nas\media`},
	} {
		t.Run(name, func(t *testing.T) {
			request := validNodeRegistration(t, "windows.tail-name.ts.net")
			request.Platform = "windows"
			request.SharedDirectories = []protocol.SharedDirectory{directory}
			if err := validateNodeRegistration(request); err != nil {
				t.Fatalf("规范 Windows 共享目录被拒绝: %v", err)
			}
		})
	}
}

func TestValidateNodeRegistrationRejectsNonCanonicalWindowsSharedDirectories(t *testing.T) {
	for name, directoryPath := range map[string]string{
		"盘符相对路径":  `C:Users\test\Downloads`,
		"正斜杠":     `C:/Users/test/Downloads`,
		"父目录段":    `C:\Users\test\..\Downloads`,
		"重复分隔符":   `C:\Users\\test\Downloads`,
		"不完整 UNC": `\\nas`,
	} {
		t.Run(name, func(t *testing.T) {
			request := validNodeRegistration(t, "windows.tail-name.ts.net")
			request.Platform = "windows"
			request.SharedDirectories = []protocol.SharedDirectory{{ID: "downloads", Name: "下载", Path: directoryPath}}
			if err := validateNodeRegistration(request); err == nil {
				t.Fatal("非规范 Windows 共享目录未被拒绝")
			}
		})
	}
}

func TestRegistrationRejectsUnsafeManagedContentConfiguration(t *testing.T) {
	_, signingKey, _ := ed25519.GenerateKey(rand.Reader)
	service, err := NewRegistrationService(NewMemoryDeviceStore(), signingKey, "control", "https://home.example.com", time.Now, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	for name, modules := range map[string][]protocol.ModuleConfig{
		"非回环地址": {{ID: "jellyfin", Enabled: true, BaseURL: "http://192.0.2.1:19446", ReadOnly: true}},
		"重复模块": {
			{ID: "filebrowser", Enabled: true, BaseURL: "http://127.0.0.1:19445", ReadOnly: true},
			{ID: "filebrowser", Enabled: true, BaseURL: "http://127.0.0.1:19445", ReadOnly: true},
		},
	} {
		t.Run(name, func(t *testing.T) {
			request := validNodeRegistration(t, "mac.tail-name.ts.net")
			request.Modules = modules
			if _, err := service.Register("owner", request); err == nil {
				t.Fatal("不安全托管内容配置未被拒绝")
			}
		})
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
