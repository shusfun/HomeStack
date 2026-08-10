package securestore

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wangshangbin/homestack/internal/managed"
	"github.com/wangshangbin/homestack/internal/protocol"
	"github.com/zalando/go-keyring"
)

func TestDeviceProfileStoresManagedContentSeparately(t *testing.T) {
	keyring.MockInit()
	profile := testDeviceProfile()
	if err := SaveDeviceProfile(profile); err != nil {
		t.Fatal(err)
	}
	storedProfile, err := keyring.Get(serviceName, deviceProfileName)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(storedProfile, "managed_content") {
		t.Fatal("设备基础档案不应内嵌托管内容")
	}
	if _, err := keyring.Get(serviceName, managedContentAccount); err != nil {
		t.Fatalf("缺少独立托管内容档案: %v", err)
	}
	loaded, err := LoadDeviceProfile()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded, profile) {
		t.Fatalf("拆分保存后档案不一致: %#v", loaded)
	}
}

func TestLoadDeviceProfileSupportsLegacyEmbeddedContent(t *testing.T) {
	keyring.MockInit()
	profile := testDeviceProfile()
	data, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	if err := keyring.Set(serviceName, deviceProfileName, string(data)); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadDeviceProfile()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded, profile) {
		t.Fatalf("旧版内嵌档案迁移读取不一致: %#v", loaded)
	}
}

func TestLoadDeviceProfileRejectsInvalidManagedContent(t *testing.T) {
	keyring.MockInit()
	profile := testDeviceProfile()
	profile.ManagedContent = nil
	data, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	if err := keyring.Set(serviceName, deviceProfileName, string(data)); err != nil {
		t.Fatal(err)
	}
	if err := keyring.Set(serviceName, managedContentAccount, "{"); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadDeviceProfile(); err == nil || !strings.Contains(err.Error(), "解析托管内容档案失败") {
		t.Fatalf("损坏的托管内容档案未返回真实错误: %v", err)
	}
}

func testDeviceProfile() DeviceProfile {
	contentRoot := "/Users/test/Library/Application Support/HomeStack/node"
	return DeviceProfile{
		DeviceID:         "device-1",
		DeviceName:       "Mac",
		ControlKeyID:     "key-1",
		ControlPublicKey: "public-key",
		SignedConfig:     "signed-config",
		Credential: protocol.DeviceCredential{
			DeviceID:    "device-1",
			DeviceToken: "device-token",
			ExpiresAt:   time.Date(2026, time.August, 10, 0, 0, 0, 0, time.UTC),
		},
		ManagedContent: &managed.Profile{
			SchemaVersion:    managed.ProfileSchema,
			StateDir:         contentRoot + "/managed",
			FileRoot:         contentRoot + "/managed/filebrowser/root",
			FileBrowser:      managed.Installation{Component: "filebrowser", Version: managed.FileBrowserVersion, ArtifactSHA256: strings.Repeat("a", 64), Executable: contentRoot + "/components/filebrowser/filebrowser", Root: contentRoot + "/components/filebrowser"},
			Jellyfin:         managed.Installation{Component: "jellyfin", Version: "10.11.11", ArtifactSHA256: strings.Repeat("b", 64), Executable: contentRoot + "/components/jellyfin/jellyfin", Root: contentRoot + "/components/jellyfin", WebDir: contentRoot + "/components/jellyfin/jellyfin-web"},
			FFmpeg:           managed.Installation{Component: "jellyfin-ffmpeg", Version: "7.1.4-3", ArtifactSHA256: strings.Repeat("c", 64), Executable: contentRoot + "/components/jellyfin-ffmpeg/ffmpeg", Root: contentRoot + "/components/jellyfin-ffmpeg"},
			JellyfinPassword: "jellyfin-password",
			ModuleSecrets:    map[string]map[string]string{"jellyfin": {"api_key": "api-key"}},
		},
	}
}
