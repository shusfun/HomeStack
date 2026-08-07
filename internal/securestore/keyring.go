package securestore

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/wangshangbin/homestack/internal/protocol"
	"github.com/wangshangbin/homestack/internal/secure"
	"github.com/zalando/go-keyring"
)

const (
	serviceName           = "HomeStack"
	deviceKeyAccount      = "device-x25519"
	deviceIdentityAccount = "device-ed25519"
	deviceProfileName     = "device-profile"
	appSessionAccount     = "app-session"
)

type AppSession struct {
	ControlURL       string    `json:"control_url"`
	AccessToken      string    `json:"access_token"`
	AccessExpiresAt  time.Time `json:"access_expires_at"`
	RefreshToken     string    `json:"refresh_token"`
	RefreshExpiresAt time.Time `json:"refresh_expires_at"`
}

func LoadOrCreateDeviceIdentityKey() (ed25519.PrivateKey, error) {
	encoded, err := keyring.Get(serviceName, deviceIdentityAccount)
	if err == nil {
		keyBytes, decodeErr := base64.RawURLEncoding.DecodeString(encoded)
		if decodeErr != nil || len(keyBytes) != ed25519.PrivateKeySize {
			return nil, errors.New("系统安全存储中的设备身份密钥无效")
		}
		return ed25519.PrivateKey(keyBytes), nil
	}
	if !errors.Is(err, keyring.ErrNotFound) {
		return nil, fmt.Errorf("读取设备身份密钥失败: %w", err)
	}
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("生成设备身份密钥失败: %w", err)
	}
	if err := keyring.Set(serviceName, deviceIdentityAccount, base64.RawURLEncoding.EncodeToString(privateKey)); err != nil {
		return nil, fmt.Errorf("保存设备身份密钥失败，不允许明文降级: %w", err)
	}
	return privateKey, nil
}

func SaveAppSession(session AppSession) error {
	if session.ControlURL == "" || session.AccessToken == "" || session.RefreshToken == "" || session.AccessExpiresAt.IsZero() || session.RefreshExpiresAt.IsZero() {
		return errors.New("App 登录凭据不完整")
	}
	data, err := json.Marshal(session)
	if err != nil {
		return fmt.Errorf("编码 App 登录凭据失败: %w", err)
	}
	if err := keyring.Set(serviceName, appSessionAccount, string(data)); err != nil {
		return fmt.Errorf("保存 App 登录凭据失败，不允许明文降级: %w", err)
	}
	return nil
}

func LoadAppSession() (AppSession, error) {
	data, err := keyring.Get(serviceName, appSessionAccount)
	if err != nil {
		return AppSession{}, fmt.Errorf("读取 App 登录凭据失败: %w", err)
	}
	var session AppSession
	if err := json.Unmarshal([]byte(data), &session); err != nil {
		return AppSession{}, fmt.Errorf("解析 App 登录凭据失败: %w", err)
	}
	if session.ControlURL == "" || session.AccessToken == "" || session.RefreshToken == "" || session.AccessExpiresAt.IsZero() || session.RefreshExpiresAt.IsZero() {
		return AppSession{}, errors.New("App 登录凭据不完整")
	}
	return session, nil
}

func HasAppSession() (bool, error) {
	_, err := keyring.Get(serviceName, appSessionAccount)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, keyring.ErrNotFound) {
		return false, nil
	}
	return false, fmt.Errorf("检查 App 登录凭据失败: %w", err)
}

func DeleteAppSession() error {
	if err := keyring.Delete(serviceName, appSessionAccount); err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return fmt.Errorf("删除 App 登录凭据失败: %w", err)
	}
	return nil
}

type DeviceProfile struct {
	DeviceID         string                    `json:"device_id"`
	DeviceName       string                    `json:"device_name"`
	ControlKeyID     string                    `json:"control_key_id"`
	ControlPublicKey string                    `json:"control_public_key"`
	SignedConfig     string                    `json:"signed_config"`
	Credential       protocol.DeviceCredential `json:"credential"`
}

func LoadOrCreateDeviceKey() (*ecdh.PrivateKey, error) {
	encoded, err := keyring.Get(serviceName, deviceKeyAccount)
	if err == nil {
		keyBytes, decodeErr := base64.RawURLEncoding.DecodeString(encoded)
		if decodeErr != nil {
			return nil, fmt.Errorf("解码系统安全存储中的设备密钥失败: %w", decodeErr)
		}
		privateKey, keyErr := ecdh.X25519().NewPrivateKey(keyBytes)
		if keyErr != nil {
			return nil, fmt.Errorf("解析系统安全存储中的设备密钥失败: %w", keyErr)
		}
		return privateKey, nil
	}
	if !errors.Is(err, keyring.ErrNotFound) {
		return nil, fmt.Errorf("读取系统安全存储失败: %w", err)
	}
	privateKey, err := secure.GenerateX25519Key()
	if err != nil {
		return nil, err
	}
	if err := keyring.Set(serviceName, deviceKeyAccount, base64.RawURLEncoding.EncodeToString(privateKey.Bytes())); err != nil {
		return nil, fmt.Errorf("写入系统安全存储失败，不允许明文降级: %w", err)
	}
	return privateKey, nil
}

func SaveDeviceProfile(profile DeviceProfile) error {
	if profile.DeviceID == "" || profile.ControlKeyID == "" || profile.ControlPublicKey == "" || profile.SignedConfig == "" || profile.Credential.DeviceToken == "" {
		return errors.New("设备安全档案不完整")
	}
	data, err := json.Marshal(profile)
	if err != nil {
		return fmt.Errorf("编码设备安全档案失败: %w", err)
	}
	if err := keyring.Set(serviceName, deviceProfileName, string(data)); err != nil {
		return fmt.Errorf("保存设备安全档案失败，不允许明文降级: %w", err)
	}
	return nil
}

func LoadDeviceProfile() (DeviceProfile, error) {
	data, err := keyring.Get(serviceName, deviceProfileName)
	if err != nil {
		return DeviceProfile{}, fmt.Errorf("读取设备安全档案失败: %w", err)
	}
	var profile DeviceProfile
	if err := json.Unmarshal([]byte(data), &profile); err != nil {
		return DeviceProfile{}, fmt.Errorf("解析设备安全档案失败: %w", err)
	}
	if profile.DeviceID == "" || profile.ControlKeyID == "" || profile.ControlPublicKey == "" || profile.SignedConfig == "" || profile.Credential.DeviceToken == "" {
		return DeviceProfile{}, errors.New("设备安全档案不完整")
	}
	return profile, nil
}
