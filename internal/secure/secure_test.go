package secure

import (
	"crypto/ed25519"
	"crypto/rand"
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
	parts[1] = parts[1][:len(parts[1])-1] + "A"
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
	envelope.Ciphertext = envelope.Ciphertext[:len(envelope.Ciphertext)-1] + "A"
	if err := OpenJSON(privateKey, envelope, &target); err == nil {
		t.Fatal("被篡改的密文不应通过验证")
	}
}
