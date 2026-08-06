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
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/wangshangbin/homestack/internal/buildinfo"
	"github.com/wangshangbin/homestack/internal/components"
	"github.com/wangshangbin/homestack/internal/control"
	"github.com/wangshangbin/homestack/internal/invite"
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
	headscaleSpec, err := components.FindSpec("headscale")
	if err != nil {
		return err
	}
	if err := components.RequireVersion(ctx, headscaleSpec); err != nil {
		return err
	}
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
	invites, err := invite.Open(filepath.Join(settings.stateDir, "invites.json"))
	if err != nil {
		return err
	}
	devices, err := control.OpenDeviceStore(filepath.Join(settings.stateDir, "devices.json"))
	if err != nil {
		return err
	}
	headscale, err := control.NewHeadscaleCLI(settings.headscaleConfig)
	if err != nil {
		return err
	}
	signingKey, err := loadPrivateKey(settings.signingKeyPath)
	if err != nil {
		return err
	}
	server, err := control.NewServer(control.ServerOptions{
		Authenticator: authenticator, Owners: owners, Invites: invites, Devices: devices, Headscale: headscale,
		SigningKey: signingKey, SigningKeyID: settings.signingKeyID, PublicURL: settings.publicURL,
		HeadscaleURL: settings.headscaleURL,
	})
	if err != nil {
		return err
	}
	log.Printf("HomeStack Control 正在监听 %s", settings.address)
	return control.ServeTLS(ctx, settings.address, settings.tlsCert, settings.tlsKey, server.Handler(web.Handler()))
}

type controlSettings struct {
	address, publicURL, headscaleURL, stateDir, headscaleConfig, tlsCert, tlsKey string
	signingKeyPath, signingKeyID                                                 string
	pocketIssuer, pocketClientID, pocketClientSecret                             string
	googleClientID, googleClientSecret                                           string
	githubClientID, githubClientSecret                                           string
}

func loadSettings() (controlSettings, error) {
	values := map[string]string{
		"HOMESTACK_CONTROL_ADDR":     os.Getenv("HOMESTACK_CONTROL_ADDR"),
		"HOMESTACK_PUBLIC_URL":       os.Getenv("HOMESTACK_PUBLIC_URL"),
		"HOMESTACK_HEADSCALE_URL":    os.Getenv("HOMESTACK_HEADSCALE_URL"),
		"HOMESTACK_STATE_DIR":        os.Getenv("HOMESTACK_STATE_DIR"),
		"HOMESTACK_HEADSCALE_CONFIG": os.Getenv("HOMESTACK_HEADSCALE_CONFIG"),
		"HOMESTACK_TLS_CERT":         os.Getenv("HOMESTACK_TLS_CERT"),
		"HOMESTACK_TLS_KEY":          os.Getenv("HOMESTACK_TLS_KEY"),
		"HOMESTACK_SIGNING_KEY":      os.Getenv("HOMESTACK_SIGNING_KEY"),
		"HOMESTACK_SIGNING_KEY_ID":   os.Getenv("HOMESTACK_SIGNING_KEY_ID"),
	}
	for name, value := range values {
		if strings.TrimSpace(value) == "" {
			return controlSettings{}, fmt.Errorf("必须设置环境变量 %s", name)
		}
	}
	return controlSettings{
		address: values["HOMESTACK_CONTROL_ADDR"], publicURL: values["HOMESTACK_PUBLIC_URL"],
		headscaleURL: values["HOMESTACK_HEADSCALE_URL"],
		stateDir:     values["HOMESTACK_STATE_DIR"], headscaleConfig: values["HOMESTACK_HEADSCALE_CONFIG"],
		tlsCert: values["HOMESTACK_TLS_CERT"], tlsKey: values["HOMESTACK_TLS_KEY"],
		signingKeyPath: values["HOMESTACK_SIGNING_KEY"], signingKeyID: values["HOMESTACK_SIGNING_KEY_ID"],
		pocketIssuer: os.Getenv("HOMESTACK_POCKET_ID_ISSUER"), pocketClientID: os.Getenv("HOMESTACK_POCKET_ID_CLIENT_ID"),
		pocketClientSecret: os.Getenv("HOMESTACK_POCKET_ID_CLIENT_SECRET"), googleClientID: os.Getenv("HOMESTACK_GOOGLE_CLIENT_ID"),
		googleClientSecret: os.Getenv("HOMESTACK_GOOGLE_CLIENT_SECRET"), githubClientID: os.Getenv("HOMESTACK_GITHUB_CLIENT_ID"),
		githubClientSecret: os.Getenv("HOMESTACK_GITHUB_CLIENT_SECRET"),
	}, nil
}

func createProviders(ctx context.Context, settings controlSettings) ([]*control.OAuthProvider, error) {
	providers := make([]*control.OAuthProvider, 0, 3)
	if settings.pocketIssuer != "" || settings.pocketClientID != "" || settings.pocketClientSecret != "" {
		provider, err := control.NewOIDCProvider(ctx, "pocket", "Pocket ID", settings.pocketIssuer, settings.pocketClientID, settings.pocketClientSecret, settings.publicURL)
		if err != nil {
			return nil, err
		}
		providers = append(providers, provider)
	}
	if settings.googleClientID != "" || settings.googleClientSecret != "" {
		provider, err := control.NewOIDCProvider(ctx, "google", "Google", "https://accounts.google.com", settings.googleClientID, settings.googleClientSecret, settings.publicURL)
		if err != nil {
			return nil, err
		}
		providers = append(providers, provider)
	}
	if settings.githubClientID != "" || settings.githubClientSecret != "" {
		provider, err := control.NewGitHubProvider(settings.githubClientID, settings.githubClientSecret, settings.publicURL)
		if err != nil {
			return nil, err
		}
		providers = append(providers, provider)
	}
	if len(providers) == 0 {
		return nil, errors.New("必须完整配置 Pocket ID、Google 或 GitHub 中至少一种登录方式")
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
