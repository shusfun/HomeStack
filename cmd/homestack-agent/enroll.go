package main

import (
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/wangshangbin/homestack/internal/protocol"
	"github.com/wangshangbin/homestack/internal/secure"
	"github.com/wangshangbin/homestack/internal/securestore"
	"github.com/wangshangbin/homestack/internal/tailscale"
)

type enrollmentMetadata struct {
	SigningKeyID     string `json:"signing_key_id"`
	SigningPublicKey string `json:"signing_public_key"`
}

func enroll(arguments []string) error {
	flags := flag.NewFlagSet("enroll", flag.ContinueOnError)
	descriptorRaw := flags.String("descriptor", "", "十分钟单次配对描述符")
	deviceName := flags.String("name", "", "与配对策略一致的设备名称")
	agentURL := flags.String("agent-url", "", "与配对策略一致的 Agent HTTPS 地址")
	credentialPath := flags.String("credential-output", defaultCredentialOutput(), "systemd 加密凭据输出路径")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if *descriptorRaw == "" || *deviceName == "" || *agentURL == "" || *credentialPath == "" {
		return errors.New("enroll 必须明确指定 --descriptor、--name、--agent-url 和 --credential-output")
	}
	descriptor, err := protocol.ParseJoinDescriptor(*descriptorRaw)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	client := &http.Client{Timeout: 30 * time.Second}
	metadata, err := fetchEnrollmentMetadata(ctx, client, descriptor.Server)
	if err != nil {
		return err
	}
	privateKey, err := secure.GenerateX25519Key()
	if err != nil {
		return err
	}
	response, err := exchangeEnrollment(ctx, client, descriptor, *deviceName, *agentURL, privateKey)
	if err != nil {
		return err
	}
	var credential protocol.DeviceCredentialV1
	if err := secure.OpenJSON(privateKey, response.SealedCredential, &credential); err != nil {
		return fmt.Errorf("解密设备凭据失败: %w", err)
	}
	if credential.DeviceID != response.DeviceID || !time.Now().UTC().Before(credential.ExpiresAt) {
		return errors.New("Control 返回的设备凭据无效或已过期")
	}
	publicKey, err := base64.RawURLEncoding.DecodeString(metadata.SigningPublicKey)
	if err != nil || len(publicKey) != ed25519.PublicKeySize {
		return errors.New("Control 签名公钥无效")
	}
	var config protocol.SignedDeviceConfigV1
	if err := secure.VerifyJWS(response.SignedConfig, ed25519.PublicKey(publicKey), metadata.SigningKeyID, &config); err != nil {
		return fmt.Errorf("验证 Control 签名配置失败: %w", err)
	}
	if config.DeviceID != response.DeviceID || config.DeviceName != *deviceName || config.AgentURL != *agentURL {
		return errors.New("Control 签名配置与配对参数不一致")
	}
	tailnet, err := tailscale.New()
	if err != nil {
		return err
	}
	if err := tailnet.VerifyVersion(ctx); err != nil {
		return err
	}
	if err := tailnet.Up(ctx, credential.HeadscaleLoginServer, credential.HeadscaleAuthKey); err != nil {
		return err
	}
	if err := tailnet.VerifyNetworkPolicy(ctx); err != nil {
		return err
	}
	credential.HeadscaleAuthKey = ""
	profile := securestore.DeviceProfile{
		DeviceID: response.DeviceID, DeviceName: response.DeviceName, ControlKeyID: metadata.SigningKeyID,
		ControlPublicKey: metadata.SigningPublicKey, SignedConfig: response.SignedConfig, Credential: credential,
	}
	if err := encryptSystemdCredential(profile, *credentialPath); err != nil {
		return err
	}
	fmt.Printf("设备 %s 已加入 Tailnet，凭据已写入 %s\n", response.DeviceName, *credentialPath)
	return nil
}

func defaultCredentialOutput() string {
	config, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(config, "credstore.encrypted", "homestack-agent-profile")
}

func fetchEnrollmentMetadata(ctx context.Context, client *http.Client, server string) (enrollmentMetadata, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server+"/api/v1/meta", nil)
	if err != nil {
		return enrollmentMetadata{}, err
	}
	response, err := client.Do(request)
	if err != nil {
		return enrollmentMetadata{}, fmt.Errorf("读取 Control 元数据失败: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return enrollmentMetadata{}, enrollmentHTTPError("读取 Control 元数据", response)
	}
	var metadata enrollmentMetadata
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&metadata); err != nil {
		return enrollmentMetadata{}, fmt.Errorf("解析 Control 元数据失败: %w", err)
	}
	if metadata.SigningKeyID == "" || metadata.SigningPublicKey == "" {
		return enrollmentMetadata{}, errors.New("Control 元数据缺少签名公钥")
	}
	return metadata, nil
}

func exchangeEnrollment(ctx context.Context, client *http.Client, descriptor protocol.JoinDescriptorV1, name, agentURL string, privateKey *ecdh.PrivateKey) (protocol.JoinResponseV1, error) {
	body, err := json.Marshal(protocol.JoinRequestV1{
		Version: protocol.JoinVersion, Code: descriptor.Code, DeviceName: name, AgentURL: agentURL,
		EncryptionPublicKey: base64.RawURLEncoding.EncodeToString(privateKey.PublicKey().Bytes()),
	})
	if err != nil {
		return protocol.JoinResponseV1{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, descriptor.Server+"/api/v1/join/exchange", strings.NewReader(string(body)))
	if err != nil {
		return protocol.JoinResponseV1{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return protocol.JoinResponseV1{}, fmt.Errorf("兑换设备配对码失败: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		return protocol.JoinResponseV1{}, enrollmentHTTPError("兑换设备配对码", response)
	}
	var result protocol.JoinResponseV1
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&result); err != nil {
		return protocol.JoinResponseV1{}, fmt.Errorf("解析设备配对结果失败: %w", err)
	}
	return result, nil
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

func enrollmentHTTPError(action string, response *http.Response) error {
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
