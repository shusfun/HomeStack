package agent

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func TestVerifyAgentArtifactRejectsTampering(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("signed agent archive")
	digest := sha256.Sum256(data)
	path := filepath.Join(t.TempDir(), "agent.tar.gz")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	artifact := updateArtifact{
		Digest: base64.StdEncoding.EncodeToString(digest[:]), Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, digest[:])),
	}
	if err := verifyAgentArtifact(path, artifact, publicKey); err != nil {
		t.Fatalf("合法签名被拒绝: %v", err)
	}
	if err := os.WriteFile(path, append(data, '!'), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verifyAgentArtifact(path, artifact, publicKey); err == nil {
		t.Fatal("篡改的 Agent 更新必须被拒绝")
	}
}

func TestExtractAgentArchiveRequiresSingleTopLevelBinary(t *testing.T) {
	directory := t.TempDir()
	valid := filepath.Join(directory, "valid.tar.gz")
	writeTestAgentArchive(t, valid, []string{"homestack-agent"})
	if err := extractAgentArchive(valid, filepath.Join(directory, "agent")); err != nil {
		t.Fatalf("合法单文件归档被拒绝: %v", err)
	}
	invalid := filepath.Join(directory, "invalid.tar.gz")
	writeTestAgentArchive(t, invalid, []string{"homestack-agent", "extra"})
	if err := extractAgentArchive(invalid, filepath.Join(directory, "agent-invalid")); err == nil {
		t.Fatal("包含额外条目的归档必须被拒绝")
	}
}

func TestSemverNewerRejectsDowngrade(t *testing.T) {
	for _, test := range []struct {
		candidate, current string
		want               bool
	}{{"3.2.4", "3.2.3", true}, {"v3.0.0", "0.9.9", true}, {"3.2.3", "3.2.3", false}, {"3.2.2", "3.2.3", false}, {"3.2.3-beta.2", "3.2.3", false}} {
		actual, err := semverNewer(test.candidate, test.current)
		if err != nil || actual != test.want {
			t.Errorf("semverNewer(%q, %q)=%v,%v，期望 %v", test.candidate, test.current, actual, err, test.want)
		}
	}
}

func TestValidateAgentVersionOutputRejectsWrongMetadata(t *testing.T) {
	valid := []byte(`{"name":"homestack-agent","version":"3.2.3","goos":"linux","goarch":"arm64"}`)
	if err := validateAgentVersionOutput(valid, "v3.2.3", "linux", "arm64"); err != nil {
		t.Fatalf("合法版本元数据被拒绝: %v", err)
	}
	for _, invalid := range [][]byte{
		[]byte(`{"name":"other","version":"3.2.3","goos":"linux","goarch":"arm64"}`),
		[]byte(`{"name":"homestack-agent","version":"3.2.4","goos":"linux","goarch":"arm64"}`),
		[]byte(`{"name":"homestack-agent","version":"3.2.3","goos":"darwin","goarch":"arm64"}`),
		[]byte(`{"name":"homestack-agent","version":"3.2.3","goos":"linux","goarch":"amd64"}`),
	} {
		if err := validateAgentVersionOutput(invalid, "3.2.3", "linux", "arm64"); err == nil {
			t.Fatalf("错误版本元数据必须被拒绝: %s", invalid)
		}
	}
}

func writeTestAgentArchive(t *testing.T, path string, names []string) {
	t.Helper()
	var compressed bytes.Buffer
	gzipWriter := gzip.NewWriter(&compressed)
	tarWriter := tar.NewWriter(gzipWriter)
	for _, name := range names {
		data := []byte("binary")
		if err := tarWriter.WriteHeader(&tar.Header{Name: name, Mode: 0o700, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tarWriter.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, compressed.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}
