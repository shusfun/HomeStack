package managed

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateProfileRejectsLegacySchema(t *testing.T) {
	err := ValidateProfile(Profile{})
	if err == nil || !strings.Contains(err.Error(), "schema 不受支持: 0") {
		t.Fatalf("旧托管内容档案未触发重新初始化: %v", err)
	}
}

func TestExistingManagedCredentialsPreservesUpgradeState(t *testing.T) {
	managedState := filepath.Join(t.TempDir(), "managed")
	existing := &Profile{
		SchemaVersion:    ProfileSchema,
		StateDir:         managedState,
		JellyfinPassword: "existing-password",
		ModuleSecrets:    map[string]map[string]string{"jellyfin": {"api_key": "existing-key"}},
	}
	password, secrets := existingManagedCredentials(existing, managedState)
	if password != existing.JellyfinPassword || secrets["jellyfin"]["api_key"] != "existing-key" {
		t.Fatalf("升级未保留 Jellyfin 凭据: password=%v secrets=%v", password != "", len(secrets))
	}
	secrets["jellyfin"]["api_key"] = "changed"
	if existing.ModuleSecrets["jellyfin"]["api_key"] != "existing-key" {
		t.Fatal("升级凭据没有深拷贝")
	}
}

func TestExistingManagedCredentialsRejectsDifferentStateDirectory(t *testing.T) {
	existing := &Profile{SchemaVersion: ProfileSchema, StateDir: filepath.Join(t.TempDir(), "old"), JellyfinPassword: "existing-password"}
	password, secrets := existingManagedCredentials(existing, filepath.Join(t.TempDir(), "managed"))
	if password != "" || len(secrets) != 0 {
		t.Fatalf("不同状态目录不应复用凭据: password=%v secrets=%v", password != "", len(secrets))
	}
}
