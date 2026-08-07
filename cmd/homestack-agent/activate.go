package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/wangshangbin/homestack/internal/protocol"
	"github.com/wangshangbin/homestack/internal/secure"
	"github.com/wangshangbin/homestack/internal/securestore"
	"github.com/wangshangbin/homestack/internal/tailscale"
)

type controlMetadata struct {
	SigningKeyID     string `json:"signing_key_id"`
	SigningPublicKey string `json:"signing_public_key"`
}

func activate(arguments []string) error {
	flags := flag.NewFlagSet("activate", flag.ContinueOnError)
	server := flags.String("server", "", "HomeStack VPS HTTPS 地址")
	activationCode := flags.String("activation-code", "", "十分钟单次激活码")
	deviceName := flags.String("name", "", "设备名称，默认使用主机名")
	credentialPath := flags.String("credential-output", defaultCredentialOutput(), "systemd 加密凭据输出路径")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if *server == "" || *activationCode == "" || *credentialPath == "" {
		return errors.New("activate 必须明确指定 --server、--activation-code 和 --credential-output")
	}
	baseURL, err := validateServerURL(*server)
	if err != nil {
		return err
	}
	name := strings.TrimSpace(*deviceName)
	if name == "" {
		name, err = os.Hostname()
		if err != nil || name == "" {
			return errors.New("读取设备名称失败")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	tailnet, err := tailscale.New()
	if err != nil {
		return err
	}
	if err := tailnet.VerifyVersion(ctx); err != nil {
		return err
	}
	status, err := tailnet.Status(ctx)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 30 * time.Second}
	metadata, err := fetchControlMetadata(ctx, client, baseURL)
	if err != nil {
		return err
	}
	encryptionKey, err := secure.GenerateX25519Key()
	if err != nil {
		return err
	}
	_, identityKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	request := protocol.NodeRegistration{Name: name, Platform: runtime.GOOS, Architecture: runtime.GOARCH, TailscaleIP: status.TailscaleIP, MagicDNS: status.MagicDNS, DevicePublicKey: base64.RawURLEncoding.EncodeToString(identityKey.Public().(ed25519.PublicKey)), EncryptionPublicKey: base64.RawURLEncoding.EncodeToString(encryptionKey.PublicKey().Bytes())}
	response, err := exchangeActivation(ctx, client, baseURL, *activationCode, request)
	if err != nil {
		return err
	}
	var credential protocol.DeviceCredential
	if err := secure.OpenJSON(encryptionKey, response.SealedCredential, &credential); err != nil {
		return fmt.Errorf("解密设备凭据失败: %w", err)
	}
	if credential.DeviceID != response.DeviceID || !time.Now().UTC().Before(credential.ExpiresAt) {
		return errors.New("Control 返回的设备凭据无效")
	}
	publicKey, err := base64.RawURLEncoding.DecodeString(metadata.SigningPublicKey)
	if err != nil || len(publicKey) != ed25519.PublicKeySize {
		return errors.New("Control 签名公钥无效")
	}
	var config protocol.SignedDeviceConfig
	if err := secure.VerifyJWS(response.SignedConfig, ed25519.PublicKey(publicKey), metadata.SigningKeyID, &config); err != nil {
		return fmt.Errorf("验证 Control 签名配置失败: %w", err)
	}
	profile := securestore.DeviceProfile{DeviceID: response.DeviceID, DeviceName: response.DeviceName, ControlKeyID: metadata.SigningKeyID, ControlPublicKey: metadata.SigningPublicKey, SignedConfig: response.SignedConfig, Credential: credential}
	if err := encryptSystemdCredential(profile, *credentialPath); err != nil {
		return err
	}
	fmt.Printf("设备 %s 已登记，凭据已写入 %s\n", response.DeviceName, *credentialPath)
	return nil
}

func validateServerURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return "", errors.New("VPS 地址必须是无凭据、无路径的 HTTPS 地址")
	}
	parsed.Path = ""
	return strings.TrimRight(parsed.String(), "/"), nil
}

func defaultCredentialOutput() string {
	config, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(config, "credstore.encrypted", "homestack-agent-profile")
}

func fetchControlMetadata(ctx context.Context, client *http.Client, server string) (controlMetadata, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server+"/api/meta", nil)
	if err != nil {
		return controlMetadata{}, err
	}
	response, err := client.Do(request)
	if err != nil {
		return controlMetadata{}, fmt.Errorf("读取 Control 元数据失败: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return controlMetadata{}, activationHTTPError("读取 Control 元数据", response)
	}
	var metadata controlMetadata
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&metadata); err != nil {
		return controlMetadata{}, fmt.Errorf("解析 Control 元数据失败: %w", err)
	}
	if metadata.SigningKeyID == "" || metadata.SigningPublicKey == "" {
		return controlMetadata{}, errors.New("Control 元数据缺少签名公钥")
	}
	return metadata, nil
}

func exchangeActivation(ctx context.Context, client *http.Client, server, code string, node protocol.NodeRegistration) (protocol.RegistrationResponse, error) {
	body, err := json.Marshal(map[string]any{"code": strings.TrimSpace(code), "node": node})
	if err != nil {
		return protocol.RegistrationResponse{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, server+"/api/auth/app/activate", strings.NewReader(string(body)))
	if err != nil {
		return protocol.RegistrationResponse{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return protocol.RegistrationResponse{}, fmt.Errorf("兑换设备激活码失败: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		return protocol.RegistrationResponse{}, activationHTTPError("兑换设备激活码", response)
	}
	var result struct {
		Registration protocol.RegistrationResponse `json:"registration"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&result); err != nil {
		return protocol.RegistrationResponse{}, fmt.Errorf("解析设备登记结果失败: %w", err)
	}
	return result.Registration, nil
}

func encryptSystemdCredential(profile securestore.DeviceProfile, outputPath string) error {
	data, err := json.Marshal(profile)
	if err != nil {
		return fmt.Errorf("编码设备凭据失败: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o700); err != nil {
		return fmt.Errorf("创建 systemd 凭据目录失败: %w", err)
	}
	command := exec.Command("systemd-creds", "encrypt", "--uid=self", "--name=homestack-agent-profile", "-", outputPath)
	command.Stdin = strings.NewReader(string(data))
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("写入 systemd 加密凭据失败: %s", strings.TrimSpace(string(output)))
	}
	return nil
}

func activationHTTPError(action string, response *http.Response) error {
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("%s失败，HTTP %d: %w", action, response.StatusCode, err)
	}
	var payload protocol.ErrorResponse
	if json.Unmarshal(data, &payload) == nil && payload.Error.Message != "" {
		return fmt.Errorf("%s失败，HTTP %d: %s", action, response.StatusCode, payload.Error.Message)
	}
	return fmt.Errorf("%s失败，HTTP %d: %s", action, response.StatusCode, strings.TrimSpace(string(data)))
}
