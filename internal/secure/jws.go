package secure

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type protectedHeader struct {
	Algorithm string `json:"alg"`
	Type      string `json:"typ"`
	KeyID     string `json:"kid"`
}

func SignJWS(privateKey ed25519.PrivateKey, keyID string, value any) (string, error) {
	if len(privateKey) != ed25519.PrivateKeySize {
		return "", errors.New("Ed25519 私钥长度无效")
	}
	header, err := json.Marshal(protectedHeader{Algorithm: "EdDSA", Type: "JWT", KeyID: keyID})
	if err != nil {
		return "", fmt.Errorf("编码 JWS 头失败: %w", err)
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("编码 JWS 载荷失败: %w", err)
	}
	encodedHeader := base64.RawURLEncoding.EncodeToString(header)
	encodedPayload := base64.RawURLEncoding.EncodeToString(payload)
	signingInput := encodedHeader + "." + encodedPayload
	signature := ed25519.Sign(privateKey, []byte(signingInput))
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func VerifyJWS(token string, publicKey ed25519.PublicKey, expectedKeyID string, target any) error {
	if len(publicKey) != ed25519.PublicKeySize {
		return errors.New("Ed25519 公钥长度无效")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return errors.New("JWS 必须包含三个部分")
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return fmt.Errorf("解码 JWS 头失败: %w", err)
	}
	var header protectedHeader
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return fmt.Errorf("解析 JWS 头失败: %w", err)
	}
	if header.Algorithm != "EdDSA" || header.Type != "JWT" || header.KeyID != expectedKeyID {
		return errors.New("JWS 保护头不符合预期")
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return fmt.Errorf("解码 JWS 签名失败: %w", err)
	}
	if !ed25519.Verify(publicKey, []byte(parts[0]+"."+parts[1]), signature) {
		return errors.New("JWS 签名验证失败")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return fmt.Errorf("解码 JWS 载荷失败: %w", err)
	}
	if err := json.Unmarshal(payload, target); err != nil {
		return fmt.Errorf("解析 JWS 载荷失败: %w", err)
	}
	return nil
}
