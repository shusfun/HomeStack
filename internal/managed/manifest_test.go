package managed

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFetchManifestVerifiesSignature(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	manifest := Manifest{SchemaVersion: ManifestSchema, Artifacts: []Artifact{{
		Component: "filebrowser", Version: "0.3.5", Platform: "darwin", Arch: "arm64",
		URL: "https://github.com/example/filebrowser", URLs: []string{"https://github.com/example/filebrowser"}, Filename: "filebrowser", Format: "binary", Size: 10,
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
		URL: "https://github.com/example/filebrowser", URLs: []string{"https://github.com/example/filebrowser"}, Filename: "filebrowser", Format: "binary", Size: 10,
		SHA256: "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz",
	}}}
	if err := ValidateManifest(manifest); err == nil {
		t.Fatal("非十六进制 SHA-256 未被拒绝")
	}
}

func TestValidateManifestRejectsModifiedAcceleratorURL(t *testing.T) {
	official := "https://github.com/example/project/releases/download/v1/file.zip"
	base := Artifact{Component: "filebrowser", Version: "1.5.1-stable", Platform: "darwin", Arch: "arm64", URL: official, Filename: "filebrowser", Format: "binary", Size: 10, SHA256: strings.Repeat("a", 64)}
	for _, candidate := range []string{
		"https://ghproxy.net/extra/" + official,
		"https://ghproxy.net/" + official + "?token=1",
		"https://ghproxy.net:443/" + official,
		"https://ghproxy.net/" + official + "#fragment",
	} {
		artifact := base
		artifact.URLs = []string{candidate}
		if err := ValidateManifest(Manifest{SchemaVersion: ManifestSchema, Artifacts: []Artifact{artifact}}); err == nil {
			t.Fatalf("篡改后的代理 URL 未被拒绝: %s", candidate)
		}
	}
}
