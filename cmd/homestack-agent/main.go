package main

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/netip"
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

func main() {
	if buildinfo.Requested(os.Args[1:]) {
		fmt.Println(buildinfo.Output("homestack-agent", os.Args[1:]))
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "enroll" {
		if err := enroll(os.Args[2:]); err != nil {
			log.Fatal(err)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "update-helper" {
		if err := runUpdateHelper(os.Args[2:]); err != nil {
			log.Fatal(err)
		}
		return
	}
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	settings, err := loadAgentSettings()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	profile, err := loadSystemdCredentialProfile()
	if err != nil {
		return err
	}
	publicKeyBytes, err := base64.RawURLEncoding.DecodeString(profile.ControlPublicKey)
	if err != nil || len(publicKeyBytes) != ed25519.PublicKeySize {
		return errors.New("设备安全档案中的 Control 公钥无效")
	}
	configStore, err := agent.OpenConfigStore(
		filepath.Join(settings.stateDir, "device-config.json"), profile.DeviceID,
		ed25519.PublicKey(publicKeyBytes), profile.ControlKeyID,
	)
	if err != nil {
		return err
	}
	if _, exists := configStore.Current(); !exists {
		if _, err := configStore.Apply(profile.SignedConfig); err != nil {
			return err
		}
	}
	initialConfig, _ := configStore.Current()
	controlClient := &agent.ControlClient{
		BaseURL: initialConfig.ControlURL, DeviceID: profile.DeviceID, DeviceToken: profile.Credential.DeviceToken,
	}
	if err := controlClient.RefreshConfig(ctx, configStore); err != nil {
		return err
	}
	config, ok := configStore.Current()
	if !ok || !time.Now().UTC().Before(config.ExpiresAt) {
		return errors.New("Control 未提供有效的最新设备配置")
	}
	profile.SignedConfig = configStore.Signed()
	sessions, err := agent.OpenSessionStore(
		filepath.Join(settings.stateDir, "used-tickets.json"), profile.DeviceID, config.ControlURL,
		ed25519.PublicKey(publicKeyBytes), profile.ControlKeyID,
	)
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
	if err := tailnet.VerifyNetworkPolicy(ctx); err != nil {
		return err
	}
	updater, err := agent.NewAgentUpdater(buildinfo.AgentUpdateManifestURL, buildinfo.UpdatePublicKey, config.AgentURL)
	if err != nil {
		return err
	}
	agentServer, err := agent.NewServer(agent.ServerOptions{
		DeviceID: profile.DeviceID, DeviceName: profile.DeviceName, ConfigStore: configStore,
		Sessions: sessions, Tailnet: tailnet, ModuleSecrets: profile.Credential.ModuleSecrets, Updater: updater,
	})
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
	go heartbeatLoop(ctx, controlClient, configStore, agentServer)
	log.Printf("HomeStack Agent 正在监听 %s", settings.address)
	return agent.ServeTLS(ctx, settings.address, settings.tlsCert, settings.tlsKey, agentServer.Handler(web.Handler()))
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

type agentSettings struct {
	address, stateDir, tlsCert, tlsKey string
}

func loadAgentSettings() (agentSettings, error) {
	values := map[string]string{
		"HOMESTACK_AGENT_ADDR":      os.Getenv("HOMESTACK_AGENT_ADDR"),
		"HOMESTACK_AGENT_STATE_DIR": os.Getenv("HOMESTACK_AGENT_STATE_DIR"),
		"HOMESTACK_AGENT_TLS_CERT":  os.Getenv("HOMESTACK_AGENT_TLS_CERT"),
		"HOMESTACK_AGENT_TLS_KEY":   os.Getenv("HOMESTACK_AGENT_TLS_KEY"),
	}
	for name, value := range values {
		if strings.TrimSpace(value) == "" {
			return agentSettings{}, fmt.Errorf("必须设置环境变量 %s", name)
		}
	}
	if err := validateAgentAddress(values["HOMESTACK_AGENT_ADDR"]); err != nil {
		return agentSettings{}, err
	}
	return agentSettings{
		address: values["HOMESTACK_AGENT_ADDR"], stateDir: values["HOMESTACK_AGENT_STATE_DIR"],
		tlsCert: values["HOMESTACK_AGENT_TLS_CERT"], tlsKey: values["HOMESTACK_AGENT_TLS_KEY"],
	}, nil
}

func validateAgentAddress(raw string) error {
	address, err := netip.ParseAddrPort(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("HOMESTACK_AGENT_ADDR 必须是明确的 Tailscale IP 和端口: %w", err)
	}
	if address.Port() != 9443 {
		return errors.New("HOMESTACK_AGENT_ADDR 必须使用端口 9443")
	}
	ip := address.Addr().Unmap()
	tailscaleIPv4 := netip.MustParsePrefix("100.64.0.0/10")
	tailscaleIPv6 := netip.MustParsePrefix("fd7a:115c:a1e0::/48")
	if !tailscaleIPv4.Contains(ip) && !tailscaleIPv6.Contains(ip) {
		return errors.New("HOMESTACK_AGENT_ADDR 必须绑定 Tailscale 地址，禁止监听公网、局域网或全部接口")
	}
	return nil
}

func startCCConnect(ctx context.Context, stateDir string, config protocol.SignedDeviceConfigV1, secrets map[string]map[string]string) (*ccconnect.ProcessManager, error) {
	projects := make([]ccconnect.Project, 0)
	for _, module := range config.Modules {
		if module.ID != "cc-connect" || !module.Enabled {
			continue
		}
		moduleSecrets := secrets[agent.ModuleKey(module)]
		projects = append(projects, ccconnect.Project{
			Name: module.InstanceID, WorkDir: module.WorkDir,
			BotID: moduleSecrets["bot_id"], BotSecret: moduleSecrets["bot_secret"],
			AllowFrom: splitUsers(moduleSecrets["allow_from"]), AdminFrom: splitUsers(moduleSecrets["admin_from"]),
		})
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
	statusTicker := time.NewTicker(30 * time.Second)
	configTicker := time.NewTicker(30 * time.Minute)
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
			log.Printf("HomeStack Agent 状态上报失败: %v", err)
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
				log.Printf("HomeStack Agent 配置刷新失败: %v", err)
				continue
			}
			if store.Signed() != before {
				if err := server.Reload(); err != nil {
					log.Printf("HomeStack Agent 应用新配置失败: %v", err)
				}
			}
		}
	}
}
