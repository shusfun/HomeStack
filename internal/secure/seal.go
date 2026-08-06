package secure

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"

	"github.com/wangshangbin/homestack/internal/protocol"
)

const sealedEnvelopeVersion = "homestack.sealed.v1"

func GenerateX25519Key() (*ecdh.PrivateKey, error) {
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("生成 X25519 设备密钥失败: %w", err)
	}
	return key, nil
}

func SealJSON(publicKey *ecdh.PublicKey, value any) (protocol.SealedEnvelopeV1, error) {
	plaintext, err := jsonMarshal(value)
	if err != nil {
		return protocol.SealedEnvelopeV1{}, err
	}
	ephemeral, err := GenerateX25519Key()
	if err != nil {
		return protocol.SealedEnvelopeV1{}, err
	}
	shared, err := ephemeral.ECDH(publicKey)
	if err != nil {
		return protocol.SealedEnvelopeV1{}, fmt.Errorf("计算 X25519 共享密钥失败: %w", err)
	}
	ephemeralPublic := ephemeral.PublicKey().Bytes()
	key := deriveKey(shared, ephemeralPublic)
	block, err := aes.NewCipher(key)
	if err != nil {
		return protocol.SealedEnvelopeV1{}, fmt.Errorf("创建 AES 密钥失败: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return protocol.SealedEnvelopeV1{}, fmt.Errorf("创建 AES-GCM 失败: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return protocol.SealedEnvelopeV1{}, fmt.Errorf("生成封装随机数失败: %w", err)
	}
	aad := append([]byte(sealedEnvelopeVersion+":"), ephemeralPublic...)
	ciphertext := gcm.Seal(nil, nonce, plaintext, aad)
	return protocol.SealedEnvelopeV1{
		Version:            sealedEnvelopeVersion,
		EphemeralPublicKey: base64.RawURLEncoding.EncodeToString(ephemeralPublic),
		Nonce:              base64.RawURLEncoding.EncodeToString(nonce),
		Ciphertext:         base64.RawURLEncoding.EncodeToString(ciphertext),
	}, nil
}

func OpenJSON(privateKey *ecdh.PrivateKey, envelope protocol.SealedEnvelopeV1, target any) error {
	if envelope.Version != sealedEnvelopeVersion {
		return errors.New("密钥封装版本不受支持")
	}
	ephemeralBytes, err := decodeEnvelopePart("临时公钥", envelope.EphemeralPublicKey)
	if err != nil {
		return err
	}
	ephemeral, err := ecdh.X25519().NewPublicKey(ephemeralBytes)
	if err != nil {
		return fmt.Errorf("解析 X25519 临时公钥失败: %w", err)
	}
	shared, err := privateKey.ECDH(ephemeral)
	if err != nil {
		return fmt.Errorf("计算 X25519 共享密钥失败: %w", err)
	}
	block, err := aes.NewCipher(deriveKey(shared, ephemeralBytes))
	if err != nil {
		return fmt.Errorf("创建 AES 密钥失败: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return fmt.Errorf("创建 AES-GCM 失败: %w", err)
	}
	nonce, err := decodeEnvelopePart("随机数", envelope.Nonce)
	if err != nil {
		return err
	}
	ciphertext, err := decodeEnvelopePart("密文", envelope.Ciphertext)
	if err != nil {
		return err
	}
	aad := append([]byte(sealedEnvelopeVersion+":"), ephemeralBytes...)
	plaintext, err := gcm.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return errors.New("密钥封装认证失败")
	}
	if err := jsonUnmarshal(plaintext, target); err != nil {
		return err
	}
	return nil
}

func deriveKey(shared, context []byte) []byte {
	zeroSalt := make([]byte, sha256.Size)
	extract := hmac.New(sha256.New, zeroSalt)
	extract.Write(shared)
	prk := extract.Sum(nil)
	expand := hmac.New(sha256.New, prk)
	expand.Write([]byte("homestack/x25519-aesgcm/v1"))
	expand.Write(context)
	expand.Write([]byte{1})
	return expand.Sum(nil)
}

func decodeEnvelopePart(name, value string) ([]byte, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("解码封装%s失败: %w", name, err)
	}
	return decoded, nil
}
