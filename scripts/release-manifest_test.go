//go:build ignore

package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"flag"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseManifest(t *testing.T) {
	dist, privateKey, publicKey := releaseFixture(t)
	output, err := runReleaseManifest(t, dist, privateKey, publicKey)
	if err != nil {
		t.Fatalf("生成发布清单失败: %v\n%s", err, output)
	}

	manifestData, err := os.ReadFile(filepath.Join(dist, "latest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		SchemaVersion int        `json:"schemaVersion"`
		Version       string     `json:"version"`
		Artifacts     []artifact `json:"artifacts"`
	}
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.SchemaVersion != 1 || manifest.Version != "3.2.3" || len(manifest.Artifacts) != 8 {
		t.Fatalf("发布清单元数据不完整: schema=%d version=%q artifacts=%d", manifest.SchemaVersion, manifest.Version, len(manifest.Artifacts))
	}
	for _, platform := range []string{"darwin", "windows", "linux"} {
		for _, arch := range []string{"amd64", "arm64"} {
			for _, current := range manifest.Artifacts {
				if current.Platform == platform && current.Arch == arch {
					if current.Component != "desktop" {
						t.Fatalf("%s/%s 首个资产不是 desktop: %s", platform, arch, current.Component)
					}
					break
				}
			}
		}
	}
	entries, err := os.ReadDir(dist)
	if err != nil {
		t.Fatal(err)
	}
	expectedSignatures := map[string]bool{}
	for _, current := range manifest.Artifacts {
		expectedSignatures[current.Filename+".sig"] = true
		signatureData, readErr := os.ReadFile(filepath.Join(dist, current.Filename+".sig"))
		if readErr != nil {
			t.Fatalf("更新资产缺少旁路签名 %s: %v", current.Filename, readErr)
		}
		if strings.TrimSpace(string(signatureData)) != current.Signature {
			t.Fatalf("更新资产旁路签名与 latest.json 不一致: %s", current.Filename)
		}
	}
	for _, arch := range []string{"amd64", "arm64"} {
		for _, component := range []string{"control", "agent"} {
			expectedSignatures["homestack-"+component+"_3.2.3_linux_"+arch+".tar.gz.sig"] = true
		}
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".sig") {
			if !expectedSignatures[entry.Name()] {
				t.Fatalf("不应发布旁路签名: %s", entry.Name())
			}
			verifyDetachedSignature(t, dist, entry.Name(), publicKey)
			delete(expectedSignatures, entry.Name())
		}
	}
	if len(expectedSignatures) != 0 {
		t.Fatalf("缺少旁路签名: %v", expectedSignatures)
	}
	if len(entries) != 33 {
		t.Fatalf("发布文件数量错误: %d", len(entries))
	}
	for _, name := range []string{"checksums.txt", "checksums.txt.sig", "latest.json.sig"} {
		if _, statErr := os.Stat(filepath.Join(dist, name)); !os.IsNotExist(statErr) {
			t.Fatalf("不应生成 %s: %v", name, statErr)
		}
	}
}

func verifyDetachedSignature(t *testing.T, dist, signatureName string, publicKey ed25519.PublicKey) {
	t.Helper()
	assetName := strings.TrimSuffix(signatureName, ".sig")
	assetData, err := os.ReadFile(filepath.Join(dist, assetName))
	if err != nil {
		t.Fatal(err)
	}
	signatureData, err := os.ReadFile(filepath.Join(dist, signatureName))
	if err != nil {
		t.Fatal(err)
	}
	signature, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(signatureData)))
	if err != nil {
		t.Fatalf("旁路签名编码无效 %s: %v", signatureName, err)
	}
	digest := sha256.Sum256(assetData)
	if !ed25519.Verify(publicKey, digest[:], signature) {
		t.Fatalf("旁路签名校验失败: %s", signatureName)
	}
}

func TestReleaseManifestRejectsMismatchedKey(t *testing.T) {
	dist, privateKey, _ := releaseFixture(t)
	otherPublic, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	output, err := runReleaseManifest(t, dist, privateKey, otherPublic)
	if err == nil || !strings.Contains(output, "发布私钥与客户端内置公钥不匹配") {
		t.Fatalf("未拒绝不匹配密钥: err=%v output=%s", err, output)
	}
}

func TestReleaseManifestRejectsMissingAsset(t *testing.T) {
	dist, privateKey, publicKey := releaseFixture(t)
	if err := os.Remove(filepath.Join(dist, "HomeStack_3.2.3_linux_arm64.deb")); err != nil {
		t.Fatal(err)
	}
	output, err := runReleaseManifest(t, dist, privateKey, publicKey)
	if err == nil || !strings.Contains(output, "发布资产集合不完整") {
		t.Fatalf("未拒绝缺失资产: err=%v output=%s", err, output)
	}
}

func TestReleaseManifestRejectsInvalidArchive(t *testing.T) {
	dist, privateKey, publicKey := releaseFixture(t)
	path := filepath.Join(dist, "HomeStack_3.2.3_windows_arm64_update.zip")
	writeZip(t, path, map[string]string{"HomeStack.exe": "binary", "extra.txt": "unexpected"})
	output, err := runReleaseManifest(t, dist, privateKey, publicKey)
	if err == nil || !strings.Contains(output, "Windows 更新 zip 必须只含单顶层 HomeStack.exe") {
		t.Fatalf("未拒绝非法更新归档: err=%v output=%s", err, output)
	}
}

func TestReleaseManifestHelper(t *testing.T) {
	if os.Getenv("HOMESTACK_RELEASE_MANIFEST_HELPER") != "1" {
		return
	}
	separator := 0
	for index, argument := range os.Args {
		if argument == "--" {
			separator = index
			break
		}
	}
	os.Args = append([]string{"release-manifest"}, os.Args[separator+1:]...)
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	main()
}

func runReleaseManifest(t *testing.T, dist string, privateKey, publicKey []byte) (string, error) {
	t.Helper()
	arguments := []string{
		"-test.run=TestReleaseManifestHelper", "--",
		"--dist", dist,
		"--tag", "v3.2.3",
		"--repository", "shusfun/HomeStack",
		"--private-key", base64.StdEncoding.EncodeToString(privateKey),
		"--public-key", base64.StdEncoding.EncodeToString(publicKey),
	}
	command := exec.Command(os.Args[0], arguments...)
	command.Env = append(os.Environ(), "HOMESTACK_RELEASE_MANIFEST_HELPER=1")
	output, err := command.CombinedOutput()
	return string(output), err
}

func releaseFixture(t *testing.T) (string, ed25519.PrivateKey, ed25519.PublicKey) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	dist := t.TempDir()
	for _, arch := range []string{"amd64", "arm64"} {
		writeFile(t, filepath.Join(dist, "homestack-control_3.2.3_linux_"+arch+".tar.gz"), "control")
		writeFile(t, filepath.Join(dist, "homestack-agent_3.2.3_linux_"+arch+".tar.gz"), "agent")
		writeTarGzip(t, filepath.Join(dist, "homestack-agent-update_3.2.3_linux_"+arch+".tar.gz"), map[string]string{"homestack-agent": "binary"})
		writeFile(t, filepath.Join(dist, "HomeStack_3.2.3_darwin_"+arch+".dmg"), "dmg")
		writeTarGzip(t, filepath.Join(dist, "HomeStack_3.2.3_darwin_"+arch+"_update.tar.gz"), map[string]string{
			"HomeStack.app/Contents/MacOS/HomeStack": "binary",
			"HomeStack.app/Contents/Info.plist":      "plist",
		})
		writeFile(t, filepath.Join(dist, "HomeStack_3.2.3_windows_"+arch+"_setup.exe"), "installer")
		writeFile(t, filepath.Join(dist, "HomeStack_3.2.3_windows_"+arch+"_portable.zip"), "portable")
		writeZip(t, filepath.Join(dist, "HomeStack_3.2.3_windows_"+arch+"_update.zip"), map[string]string{"HomeStack.exe": "binary"})
		writeFile(t, filepath.Join(dist, "HomeStack_3.2.3_linux_"+arch+".AppImage"), "appimage")
		writeFile(t, filepath.Join(dist, "HomeStack_3.2.3_linux_"+arch+".deb"), "deb")
	}
	return dist, privateKey, publicKey
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeTarGzip(t *testing.T, path string, entries map[string]string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	compressed := gzip.NewWriter(file)
	archive := tar.NewWriter(compressed)
	for name, content := range entries {
		header := &tar.Header{Name: name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg}
		if err := archive.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(archive, content); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func writeZip(t *testing.T, path string, entries map[string]string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	for name, content := range entries {
		writer, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(writer, content); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
