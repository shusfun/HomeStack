package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/wangshangbin/homestack/internal/buildinfo"
	"github.com/wangshangbin/homestack/internal/control"
	"github.com/wangshangbin/homestack/internal/controlupdate"
	setupapi "github.com/wangshangbin/homestack/internal/setup"
	"github.com/wangshangbin/homestack/internal/web"
)

func main() {
	if buildinfo.Requested(os.Args[1:]) {
		fmt.Println(buildinfo.Output("homestack-control", os.Args[1:]))
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "keygen" {
		if err := keygen(os.Args[2:]); err != nil {
			log.Fatal(err)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "setup" {
		if err := runSetup(os.Args[2:]); err != nil {
			log.Fatal(err)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "configtest" {
		if err := configtest(os.Args[2:]); err != nil {
			log.Fatal(err)
		}
		fmt.Println("HomeStack Control 配置有效")
		return
	}
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	settings, err := loadSettings()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	owners, err := control.OpenOwnerStore(filepath.Join(settings.stateDir, "owner.json"))
	if err != nil {
		return err
	}
	providers, err := createProviders(ctx, settings)
	if err != nil {
		return err
	}
	authenticator, err := control.NewAuthManager(providers, owners)
	if err != nil {
		return err
	}
	devices, err := control.OpenDeviceStore(filepath.Join(settings.stateDir, "devices.json"))
	if err != nil {
		return err
	}
	activations, err := control.OpenActivationStore(filepath.Join(settings.stateDir, "activations.json"), time.Now, rand.Reader)
	if err != nil {
		return err
	}
	signingKey, err := loadPrivateKey(settings.signingKeyPath)
	if err != nil {
		return err
	}
	helper := setupapi.SocketClient{}
	updater, err := controlupdate.New(controlupdate.Options{
		CurrentVersion: buildinfo.Version, ManifestURL: buildinfo.UpdateManifestURL, PublicKey: buildinfo.UpdatePublicKey,
		StateDir: settings.stateDir, Installer: helper,
	})
	if err != nil {
		return err
	}
	server, err := control.NewServer(control.ServerOptions{
		Authenticator: authenticator, Owners: owners, Devices: devices, Activations: activations,
		SigningKey: signingKey, SigningKeyID: settings.signingKeyID, PublicURL: settings.publicURL,
		ConfigHelper: helper, ControlUpdater: updater,
	})
	if err != nil {
		return err
	}
	log.Printf("HomeStack Control 正在监听 %s (%s)", settings.address, settings.transport)
	return control.ServeReverseProxy(ctx, settings.address, server.Handler(web.Handler()))
}

type controlSettings struct {
	transport, address, publicURL, stateDir string
	signingKeyPath, signingKeyID            string
	providers                               map[string]setupapi.ProviderCredentials
}

func loadSettings() (controlSettings, error) {
	values := map[string]string{
		"HOMESTACK_CONTROL_TRANSPORT": os.Getenv("HOMESTACK_CONTROL_TRANSPORT"),
		"HOMESTACK_CONTROL_ADDR":      os.Getenv("HOMESTACK_CONTROL_ADDR"),
		"HOMESTACK_PUBLIC_URL":        os.Getenv("HOMESTACK_PUBLIC_URL"),
		"HOMESTACK_STATE_DIR":         os.Getenv("HOMESTACK_STATE_DIR"),
		"HOMESTACK_SIGNING_KEY":       os.Getenv("HOMESTACK_SIGNING_KEY"),
		"HOMESTACK_SIGNING_KEY_ID":    os.Getenv("HOMESTACK_SIGNING_KEY_ID"),
	}
	for name, value := range values {
		if strings.TrimSpace(value) == "" {
			return controlSettings{}, fmt.Errorf("必须设置环境变量 %s", name)
		}
	}
	transport := values["HOMESTACK_CONTROL_TRANSPORT"]
	if transport != "reverse-proxy" {
		return controlSettings{}, errors.New("HOMESTACK_CONTROL_TRANSPORT 必须设置为 reverse-proxy")
	}
	host, _, err := net.SplitHostPort(values["HOMESTACK_CONTROL_ADDR"])
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return controlSettings{}, errors.New("Control 必须绑定明确的回环 IP 和端口")
	}
	if strings.TrimSpace(os.Getenv("HOMESTACK_TLS_CERT")) != "" || strings.TrimSpace(os.Getenv("HOMESTACK_TLS_KEY")) != "" {
		return controlSettings{}, errors.New("Control 后端不允许配置 TLS 证书")
	}
	providers := map[string]setupapi.ProviderCredentials{}
	for _, provider := range []string{"google", "github"} {
		prefix := "HOMESTACK_" + strings.ToUpper(provider)
		label := "GitHub"
		if provider == "google" {
			label = "Google"
		}
		credentials := setupapi.ProviderCredentials{ClientID: strings.TrimSpace(os.Getenv(prefix + "_CLIENT_ID")), ClientSecret: strings.TrimSpace(os.Getenv(prefix + "_CLIENT_SECRET"))}
		if credentials.ClientID == "" && credentials.ClientSecret == "" {
			continue
		}
		if credentials.ClientID == "" || credentials.ClientSecret == "" {
			return controlSettings{}, fmt.Errorf("%s OAuth Client ID 和 Client Secret 必须完整配置", label)
		}
		providers[provider] = credentials
	}
	if len(providers) == 0 {
		return controlSettings{}, errors.New("必须至少配置 Google 或 GitHub 中一种登录方式")
	}
	return controlSettings{
		transport: transport, address: values["HOMESTACK_CONTROL_ADDR"], publicURL: values["HOMESTACK_PUBLIC_URL"],
		stateDir:       values["HOMESTACK_STATE_DIR"],
		signingKeyPath: values["HOMESTACK_SIGNING_KEY"], signingKeyID: values["HOMESTACK_SIGNING_KEY_ID"],
		providers: providers,
	}, nil
}

func runSetup(arguments []string) error {
	flags := flag.NewFlagSet("setup", flag.ContinueOnError)
	address := flags.String("addr", "127.0.0.1:18443", "Setup 回环监听地址")
	tokenHash := flags.String("token-hash", "/etc/homestack/setup-token.sha256", "Setup 令牌 SHA-256 文件")
	sessionPath := flags.String("session", "/etc/homestack/setup-session.json", "Setup 会话摘要文件")
	socket := flags.String("socket", setupapi.DefaultSocketPath, "Config Helper Unix Socket")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	host, _, err := net.SplitHostPort(*address)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return errors.New("Setup 必须绑定明确的回环 IP 和端口")
	}
	server, err := setupapi.NewServer(setupapi.ServerOptions{TokenHashPath: *tokenHash, SessionPath: *sessionPath, Helper: setupapi.SocketClient{Path: *socket}})
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log.Printf("HomeStack Setup 正在监听 %s", *address)
	return control.ServeReverseProxy(ctx, *address, server.Handler(web.Handler()))
}

func configtest(arguments []string) error {
	flags := flag.NewFlagSet("configtest", flag.ContinueOnError)
	envFile := flags.String("env-file", "", "Control 环境文件")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if *envFile == "" {
		return errors.New("configtest 必须指定 --env-file")
	}
	if err := loadEnvFile(*envFile); err != nil {
		return err
	}
	settings, err := loadSettings()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := createProviders(ctx, settings); err != nil {
		return err
	}
	if _, err := loadPrivateKey(settings.signingKeyPath); err != nil {
		return err
	}
	return nil
}

func loadEnvFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("读取 Control 环境文件失败: %w", err)
	}
	for index, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		name, value, found := strings.Cut(trimmed, "=")
		if !found || name == "" || strings.ContainsAny(name, " \t") {
			return fmt.Errorf("Control 环境文件第 %d 行格式无效", index+1)
		}
		if err := os.Setenv(name, value); err != nil {
			return fmt.Errorf("设置 Control 环境变量 %s 失败: %w", name, err)
		}
	}
	return nil
}

func createProviders(ctx context.Context, settings controlSettings) ([]*control.OAuthProvider, error) {
	providers := make([]*control.OAuthProvider, 0, len(settings.providers))
	if credentials, ok := settings.providers["google"]; ok {
		provider, err := control.NewOIDCProvider(ctx, "google", "Google", "https://accounts.google.com", credentials.ClientID, credentials.ClientSecret, settings.publicURL)
		if err != nil {
			return nil, err
		}
		providers = append(providers, provider)
	}
	if credentials, ok := settings.providers["github"]; ok {
		provider, err := control.NewGitHubProvider(credentials.ClientID, credentials.ClientSecret, settings.publicURL)
		if err != nil {
			return nil, err
		}
		providers = append(providers, provider)
	}
	return providers, nil
}

func keygen(arguments []string) error {
	flags := flag.NewFlagSet("keygen", flag.ContinueOnError)
	privatePath := flags.String("private", "", "Ed25519 私钥输出路径")
	publicPath := flags.String("public", "", "Ed25519 公钥输出路径")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if *privatePath == "" || *publicPath == "" {
		return errors.New("keygen 必须明确指定 --private 和 --public")
	}
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return fmt.Errorf("生成 Ed25519 密钥失败: %w", err)
	}
	if err := writeNewFile(*privatePath, []byte(base64.RawURLEncoding.EncodeToString(privateKey)+"\n"), 0o600); err != nil {
		return err
	}
	if err := writeNewFile(*publicPath, []byte(base64.RawURLEncoding.EncodeToString(publicKey)+"\n"), 0o644); err != nil {
		return err
	}
	fmt.Printf("已生成 Control 签名密钥：%s，公钥：%s\n", *privatePath, *publicPath)
	return nil
}

func loadPrivateKey(path string) (ed25519.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取 Control 签名私钥失败: %w", err)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(string(data)))
	if err != nil || len(decoded) != ed25519.PrivateKeySize {
		return nil, errors.New("Control 签名私钥必须是 base64url 编码的 Ed25519 私钥")
	}
	return ed25519.PrivateKey(decoded), nil
}

func writeNewFile(path string, data []byte, permission os.FileMode) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, permission)
	if err != nil {
		return fmt.Errorf("创建密钥文件失败: %w", err)
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return fmt.Errorf("写入密钥文件失败: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("同步密钥文件失败: %w", err)
	}
	return file.Close()
}
