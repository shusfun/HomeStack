package control

import (
	"strings"
	"testing"

	"github.com/wangshangbin/homestack/internal/protocol"
)

func TestValidateJoinPolicyAcceptsSafeModules(t *testing.T) {
	policy := validJoinPolicy()
	if err := validateJoinPolicy(policy); err != nil {
		t.Fatalf("安全连接策略应通过校验: %v", err)
	}
}

func TestValidateJoinPolicyRejectsUnsafeSecrets(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*protocol.JoinPolicyV1)
		message string
	}{
		{
			name: "缺少模块密钥",
			mutate: func(policy *protocol.JoinPolicyV1) {
				delete(policy.ModuleSecrets, "filebrowser")
			},
			message: "缺少密钥配置",
		},
		{
			name: "额外密钥字段",
			mutate: func(policy *protocol.JoinPolicyV1) {
				policy.ModuleSecrets["jellyfin"]["admin"] = "true"
			},
			message: "不允许的密钥字段",
		},
		{
			name: "孤立密钥",
			mutate: func(policy *protocol.JoinPolicyV1) {
				policy.ModuleSecrets["unused"] = map[string]string{"token": "secret"}
			},
			message: "未启用模块",
		},
		{
			name: "允许用户通配符",
			mutate: func(policy *protocol.JoinPolicyV1) {
				policy.ModuleSecrets["project-a"]["allow_from"] = "*"
			},
			message: "不允许为空或使用通配符",
		},
		{
			name: "管理员通配符",
			mutate: func(policy *protocol.JoinPolicyV1) {
				policy.ModuleSecrets["project-a"]["admin_from"] = "alice,*"
			},
			message: "不允许为空或使用通配符",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy := validJoinPolicy()
			test.mutate(&policy)
			err := validateJoinPolicy(policy)
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("期望错误包含 %q，实际为 %v", test.message, err)
			}
		})
	}
}

func TestValidateJoinPolicyRejectsInvalidModuleShape(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*protocol.JoinPolicyV1)
		message string
	}{
		{
			name: "Agent 端口错误",
			mutate: func(policy *protocol.JoinPolicyV1) {
				policy.AgentURL = "https://device.example.ts.net:9444"
			},
			message: "9443",
		},
		{
			name: "项目标识格式错误",
			mutate: func(policy *protocol.JoinPolicyV1) {
				policy.Modules[2].InstanceID = "bad project"
				policy.ModuleSecrets["bad project"] = policy.ModuleSecrets["project-a"]
				delete(policy.ModuleSecrets, "project-a")
			},
			message: "instance_id 格式无效",
		},
		{
			name: "Jellyfin 非只读",
			mutate: func(policy *protocol.JoinPolicyV1) {
				policy.Modules[1].ReadOnly = false
			},
			message: "Jellyfin 模块必须为只读",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy := validJoinPolicy()
			test.mutate(&policy)
			err := validateJoinPolicy(policy)
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("期望错误包含 %q，实际为 %v", test.message, err)
			}
		})
	}
}

func validJoinPolicy() protocol.JoinPolicyV1 {
	return protocol.JoinPolicyV1{
		DeviceName: "home-server",
		AgentURL:   "https://device.example.ts.net:9443",
		Modules: []protocol.ModuleConfigV1{
			{ID: "filebrowser", Enabled: true, BaseURL: "http://127.0.0.1:8080", ReadOnly: true},
			{ID: "jellyfin", Enabled: true, BaseURL: "http://127.0.0.1:8096", ReadOnly: true},
			{ID: "cc-connect", InstanceID: "project-a", Enabled: true, WorkDir: "/srv/projects/a"},
		},
		SharedDirectories: []protocol.SharedDirectoryV1{{ID: "default", Name: "文件", Permissions: []string{"read", "download"}}},
		ModuleSecrets: map[string]map[string]string{
			"filebrowser": {"api_token": "file-token"},
			"jellyfin":    {"api_key": "media-key"},
			"project-a": {
				"bot_id": "bot-id", "bot_secret": "bot-secret", "allow_from": "alice,bob", "admin_from": "alice",
			},
		},
	}
}
