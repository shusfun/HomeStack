package managed

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchManifestVerifiesSignature(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	manifest := Manifest{SchemaVersion: ManifestSchema, Artifacts: []Artifact{{
		Component: "filebrowser", Version: "0.3.5", Platform: "darwin", Arch: "arm64",
		URL: "https://github.com/example/filebrowser", Filename: "filebrowser", Format: "binary", Size: 10,
		SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}}}
	envelope, err := SignManifest(manifest, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { _, _ = writer.Write(envelope) }))
	defer server.Close()
	if _, err := FetchManifest(context.Background(), server.Client(), server.URL, publicKey); err != nil {
		t.Fatal(err)
	}
	envelope[len(envelope)-2] ^= 1
	server.Config.Handler = http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { _, _ = writer.Write(envelope) })
	if _, err := FetchManifest(context.Background(), server.Client(), server.URL, publicKey); err == nil {
		t.Fatal("被篡改的组件清单未被拒绝")
	}
}

func TestValidateManifestRejectsNonHexDigest(t *testing.T) {
	manifest := Manifest{SchemaVersion: ManifestSchema, Artifacts: []Artifact{{
		Component: "filebrowser", Version: "0.3.5", Platform: "darwin", Arch: "arm64",
		URL: "https://github.com/example/filebrowser", Filename: "filebrowser", Format: "binary", Size: 10,
		SHA256: "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz",
	}}}
	if err := ValidateManifest(manifest); err == nil {
		t.Fatal("非十六进制 SHA-256 未被拒绝")
	}
}
