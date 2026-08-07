package node

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/wangshangbin/homestack/internal/agent"
	"github.com/wangshangbin/homestack/internal/buildinfo"
	"github.com/wangshangbin/homestack/internal/ccconnect"
	"github.com/wangshangbin/homestack/internal/protocol"
	"github.com/wangshangbin/homestack/internal/securestore"
	"github.com/wangshangbin/homestack/internal/tailscale"
	"github.com/wangshangbin/homestack/internal/web"
)

func Run() error {
	settings, err := loadSettings()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	profile, err := loadDeviceProfile()
	if err != nil {
		return err
	}
	publicKeyBytes, err := base64.RawURLEncoding.DecodeString(profile.ControlPublicKey)
	if err != nil || len(publicKeyBytes) != ed25519.PublicKeySize {
		return errors.New("设备安全档案中的 Control 公钥无效")
	}
	configStore, err := agent.OpenConfigStore(filepath.Join(settings.stateDir, "device-config.json"), profile.DeviceID, ed25519.PublicKey(publicKeyBytes), profile.ControlKeyID)
	if err != nil {
		return err
	}
	if _, exists := configStore.Current(); !exists {
		if _, err := configStore.Apply(profile.SignedConfig); err != nil {
			return err
		}
	}
	initialConfig, _ := configStore.Current()
	controlClient := &agent.ControlClient{BaseURL: initialConfig.ControlURL, DeviceID: profile.DeviceID, DeviceToken: profile.Credential.DeviceToken}
	if err := controlClient.RefreshConfig(ctx, configStore); err != nil {
		return err
	}
	config, ok := configStore.Current()
	if !ok || !time.Now().UTC().Before(config.ExpiresAt) {
		return errors.New("Control 未提供有效的最新设备配置")
	}
	profile.SignedConfig = configStore.Signed()
	sessions, err := agent.OpenSessionStore(filepath.Join(settings.stateDir, "used-tickets.json"), profile.DeviceID, config.ControlURL, ed25519.PublicKey(publicKeyBytes), profile.ControlKeyID)
	if err != nil {
		return err
	}
	tailnet, err := tailscale.New()
	if err != nil {
		return err
	}
	if err := tailnet.VerifyVersion(ctx); err != nil {
		return err
	}
	if _, err := tailnet.Status(ctx); err != nil {
		return err
	}
	if err := tailnet.EnsureServe(ctx); err != nil {
		return err
	}
	var updater agent.Updater
	if os.Getenv("HOMESTACK_NODE_PROFILE_SOURCE") == "keyring" {
		updater = agent.DisabledUpdater{}
	} else {
		updater, err = agent.NewAgentUpdater(buildinfo.AgentUpdateManifestURL, buildinfo.UpdatePublicKey, config.AgentURL)
		if err != nil {
			return err
		}
	}
	server, err := agent.NewServer(agent.ServerOptions{DeviceID: profile.DeviceID, DeviceName: profile.DeviceName, ConfigStore: configStore, Sessions: sessions, Tailnet: tailnet, ModuleSecrets: profile.Credential.ModuleSecrets, Updater: updater})
	if err != nil {
		return err
	}
	ccManager, err := startCCConnect(ctx, settings.stateDir, config, profile.Credential.ModuleSecrets)
	if err != nil {
		return err
	}
	if ccManager != nil {
		defer func() {
			stopContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if stopErr := ccManager.Stop(stopContext); stopErr != nil && !errors.Is(stopErr, context.DeadlineExceeded) {
				log.Printf("停止 cc-connect 失败: %v", stopErr)
			}
		}()
	}
	go heartbeatLoop(ctx, controlClient, configStore, server)
	log.Printf("HomeStack Node 正在监听 %s", settings.address)
	return agent.ServeHTTP(ctx, settings.address, server.Handler(web.Handler()))
}

func loadDeviceProfile() (securestore.DeviceProfile, error) {
	switch os.Getenv("HOMESTACK_NODE_PROFILE_SOURCE") {
	case "systemd":
		return loadSystemdCredentialProfile()
	case "keyring":
		return securestore.LoadDeviceProfile()
	default:
		return securestore.DeviceProfile{}, errors.New("HOMESTACK_NODE_PROFILE_SOURCE 必须明确设置为 systemd 或 keyring")
	}
}

func loadSystemdCredentialProfile() (securestore.DeviceProfile, error) {
	directory := os.Getenv("CREDENTIALS_DIRECTORY")
	if directory == "" {
		return securestore.DeviceProfile{}, errors.New("systemd 未注入 CREDENTIALS_DIRECTORY")
	}
	data, err := os.ReadFile(filepath.Join(directory, "homestack-agent-profile"))
	if err != nil {
		return securestore.DeviceProfile{}, fmt.Errorf("读取 systemd-creds 设备档案失败: %w", err)
	}
	var profile securestore.DeviceProfile
	if err := json.Unmarshal(data, &profile); err != nil {
		return securestore.DeviceProfile{}, fmt.Errorf("解析 systemd-creds 设备档案失败: %w", err)
	}
	if profile.DeviceID == "" || profile.DeviceName == "" || profile.ControlKeyID == "" || profile.ControlPublicKey == "" || profile.SignedConfig == "" || profile.Credential.DeviceToken == "" {
		return securestore.DeviceProfile{}, errors.New("systemd-creds 设备档案不完整")
	}
	return profile, nil
}

type settings struct{ address, stateDir string }

func loadSettings() (settings, error) {
	address, stateDir := os.Getenv("HOMESTACK_AGENT_ADDR"), os.Getenv("HOMESTACK_AGENT_STATE_DIR")
	if strings.TrimSpace(address) == "" || strings.TrimSpace(stateDir) == "" {
		return settings{}, errors.New("必须设置 HOMESTACK_AGENT_ADDR 和 HOMESTACK_AGENT_STATE_DIR")
	}
	if err := ValidateAddress(address); err != nil {
		return settings{}, err
	}
	return settings{address: address, stateDir: stateDir}, nil
}

func ValidateAddress(raw string) error {
	host, port, err := net.SplitHostPort(strings.TrimSpace(raw))
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() || port != "19444" {
		return errors.New("HOMESTACK_AGENT_ADDR 必须绑定明确的回环地址 19444")
	}
	return nil
}

func startCCConnect(ctx context.Context, stateDir string, config protocol.SignedDeviceConfig, secrets map[string]map[string]string) (*ccconnect.ProcessManager, error) {
	projects := make([]ccconnect.Project, 0)
	for _, module := range config.Modules {
		if module.ID != "cc-connect" || !module.Enabled {
			continue
		}
		values := secrets[agent.ModuleKey(module)]
		projects = append(projects, ccconnect.Project{Name: module.InstanceID, WorkDir: module.WorkDir, BotID: values["bot_id"], BotSecret: values["bot_secret"], AllowFrom: splitUsers(values["allow_from"]), AdminFrom: splitUsers(values["admin_from"])})
	}
	if len(projects) == 0 {
		return nil, nil
	}
	configPath := filepath.Join(stateDir, "cc-connect", "config.toml")
	if err := ccconnect.WriteConfig(configPath, projects); err != nil {
		return nil, err
	}
	manager := ccconnect.NewProcessManager("cc-connect", configPath, os.Stderr)
	if err := manager.Start(ctx); err != nil {
		return nil, err
	}
	return manager, nil
}

func splitUsers(value string) []string {
	parts := strings.Split(value, ",")
	users := make([]string, 0, len(parts))
	for _, part := range parts {
		if user := strings.TrimSpace(part); user != "" {
			users = append(users, user)
		}
	}
	return users
}

func heartbeatLoop(ctx context.Context, client *agent.ControlClient, store *agent.ConfigStore, server *agent.Server) {
	statusTicker, configTicker := time.NewTicker(30*time.Second), time.NewTicker(30*time.Minute)
	defer statusTicker.Stop()
	defer configTicker.Stop()
	postStatus := func() {
		statusContext, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		status, err := server.BuildStatus(statusContext)
		if err == nil {
			err = client.PostStatus(statusContext, status)
		}
		if err != nil {
			log.Printf("HomeStack Node 状态上报失败: %v", err)
		}
	}
	postStatus()
	for {
		select {
		case <-ctx.Done():
			return
		case <-statusTicker.C:
			postStatus()
		case <-configTicker.C:
			refreshContext, cancel := context.WithTimeout(ctx, 30*time.Second)
			before := store.Signed()
			err := client.RefreshConfig(refreshContext, store)
			cancel()
			if err != nil {
				log.Printf("HomeStack Node 配置刷新失败: %v", err)
				continue
			}
			if store.Signed() != before {
				if err := server.Reload(); err != nil {
					log.Printf("HomeStack Node 应用新配置失败: %v", err)
				}
			}
		}
	}
}
