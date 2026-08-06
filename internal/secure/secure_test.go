package secure

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"
)

type testPayload struct {
	DeviceID string `json:"device_id"`
}

func TestJWSTamperIsRejected(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	token, err := SignJWS(privateKey, "control-1", testPayload{DeviceID: "device-1"})
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(token, ".")
	parts[1] = tamperBase64URL(t, parts[1])
	var target testPayload
	if err := VerifyJWS(strings.Join(parts, "."), publicKey, "control-1", &target); err == nil {
		t.Fatal("被篡改的 JWS 不应通过验证")
	}
}

func TestX25519SealRoundTripAndTamper(t *testing.T) {
	privateKey, err := GenerateX25519Key()
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := SealJSON(privateKey.PublicKey(), testPayload{DeviceID: "device-1"})
	if err != nil {
		t.Fatal(err)
	}
	var target testPayload
	if err := OpenJSON(privateKey, envelope, &target); err != nil {
		t.Fatal(err)
	}
	if target.DeviceID != "device-1" {
		t.Fatalf("解封结果错误: %#v", target)
	}
	envelope.Ciphertext = tamperBase64URL(t, envelope.Ciphertext)
	if err := OpenJSON(privateKey, envelope, &target); err == nil {
		t.Fatal("被篡改的密文不应通过验证")
	}
}

func tamperBase64URL(t *testing.T, value string) string {
	t.Helper()
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded) == 0 {
		t.Fatal("测试数据不能为空")
	}
	decoded[0] ^= 0x01
	return base64.RawURLEncoding.EncodeToString(decoded)
}
