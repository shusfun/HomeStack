package securestore

import (
	"crypto/ecdh"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/wangshangbin/homestack/internal/protocol"
	"github.com/wangshangbin/homestack/internal/secure"
	"github.com/zalando/go-keyring"
)

const (
	serviceName       = "HomeStack"
	deviceKeyAccount  = "device-x25519-v1"
	deviceProfileName = "device-profile-v1"
)

type DeviceProfile struct {
	DeviceID         string                      `json:"device_id"`
	DeviceName       string                      `json:"device_name"`
	ControlKeyID     string                      `json:"control_key_id"`
	ControlPublicKey string                      `json:"control_public_key"`
	SignedConfig     string                      `json:"signed_config"`
	Credential       protocol.DeviceCredentialV1 `json:"credential"`
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
