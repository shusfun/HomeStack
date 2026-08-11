//go:build ignore

package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type artifact struct {
	Component     string `json:"component"`
	URL           string `json:"url"`
	Filename      string `json:"filename"`
	Filetype      string `json:"filetype"`
	Size          int64  `json:"size"`
	Platform      string `json:"platform"`
	Arch          string `json:"arch"`
	DigestAlgo    string `json:"digestAlgo"`
	Digest        string `json:"digest"`
	SignatureAlgo string `json:"signatureAlgo"`
	Signature     string `json:"signature"`
}

func main() {
	dist := flag.String("dist", "dist", "发布资产目录")
	tag := flag.String("tag", "", "发布标签")
	repository := flag.String("repository", "", "GitHub owner/repo")
	privateEncoded := flag.String("private-key", "", "base64 Ed25519 私钥")
	publicEncoded := flag.String("public-key", "", "base64 Ed25519 公钥")
	flag.Parse()
	if *tag == "" || *repository == "" || *privateEncoded == "" || *publicEncoded == "" {
		fatal(errors.New("tag、repository、private-key 和 public-key 必须明确提供"))
	}
	privateKey := decodeKey(*privateEncoded, ed25519.PrivateKeySize, "私钥")
	publicKey := decodeKey(*publicEncoded, ed25519.PublicKeySize, "公钥")
	if !ed25519.PublicKey(privateKey[32:]).Equal(ed25519.PublicKey(publicKey)) {
		fatal(errors.New("发布私钥与客户端内置公钥不匹配"))
	}
	entries, err := os.ReadDir(*dist)
	if err != nil {
		fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		if strings.HasSuffix(entry.Name(), ".sig") || entry.Name() == "checksums.txt" || entry.Name() == "latest.json" {
			if err := os.Remove(filepath.Join(*dist, entry.Name())); err != nil {
				fatal(fmt.Errorf("清理旧发布元数据失败: %w", err))
			}
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	version := strings.TrimPrefix(*tag, "v")
	validateAssetSet(names, version)
	artifacts := make([]artifact, 0, 10)
	for _, name := range names {
		path := filepath.Join(*dist, name)
		if err := validateUpdateArchive(path, name); err != nil {
			fatal(err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			fatal(err)
		}
		digest := sha256.Sum256(data)
		signature := ed25519.Sign(ed25519.PrivateKey(privateKey), digest[:])
		current, updateAsset := manifestArtifact(name, version, *repository, *tag, int64(len(data)), digest[:], signature)
		if updateAsset {
			artifacts = append(artifacts, current)
		}
		if updateAsset || isInstallerAsset(name, version) {
			writeDetachedSignature(path, signature)
		}
	}
	if len(artifacts) != 10 {
		fatal(fmt.Errorf("latest.json 必须包含 10 个更新资产，实际为 %d", len(artifacts)))
	}
	// Wails Endpoint Provider 取同平台/架构的首个资产，桌面载荷必须排在 Agent 之前。
	sort.SliceStable(artifacts, func(left, right int) bool {
		if artifacts[left].Component != artifacts[right].Component {
			return artifacts[left].Component == "desktop"
		}
		if artifacts[left].Platform != artifacts[right].Platform {
			return artifacts[left].Platform < artifacts[right].Platform
		}
		return artifacts[left].Arch < artifacts[right].Arch
	})
	manifest := map[string]any{
		"schemaVersion": 1, "version": version, "channel": "stable", "name": "HomeStack " + version,
		"publishedAt": time.Now().UTC().Format(time.RFC3339), "notes": "请查看 GitHub Release 更新说明。", "artifacts": artifacts,
	}
	manifestData, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		fatal(err)
	}
	manifestData = append(manifestData, '\n')
	if err := os.WriteFile(filepath.Join(*dist, "latest.json"), manifestData, 0o644); err != nil {
		fatal(err)
	}
}

func validateUpdateArchive(path, name string) error {
	switch {
	case strings.Contains(name, "_darwin_") && strings.HasSuffix(name, "_update.tar.gz"):
		return validateTarTopLevel(path, "HomeStack.app", "HomeStack.app/Contents/MacOS/HomeStack", false)
	case strings.HasPrefix(name, "homestack-agent-update_"):
		return validateTarTopLevel(path, "homestack-agent", "homestack-agent", true)
	case strings.HasPrefix(name, "homestack-control-update_"):
		return validateControlUpdateTar(path)
	case strings.Contains(name, "_windows_") && strings.HasSuffix(name, "_update.zip"):
		reader, err := zip.OpenReader(path)
		if err != nil {
			return fmt.Errorf("打开 Windows 更新 zip 失败: %w", err)
		}
		defer reader.Close()
		if len(reader.File) != 1 || filepath.ToSlash(reader.File[0].Name) != "HomeStack.exe" || reader.File[0].FileInfo().IsDir() {
			return errors.New("Windows 更新 zip 必须只含单顶层 HomeStack.exe")
		}
	}
	return nil
}

func validateControlUpdateTar(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	compressed, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("打开 Control 更新 gzip 失败: %w", err)
	}
	defer compressed.Close()
	reader := tar.NewReader(compressed)
	expected := map[string]bool{"homestack-control": false, "homestack-config-helper": false}
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("读取 Control 更新 tar 失败: %w", err)
		}
		if _, ok := expected[header.Name]; !ok || expected[header.Name] || header.Typeflag != tar.TypeReg || header.Size <= 0 {
			return fmt.Errorf("Control 更新 tar 包含非法条目: %s", header.Name)
		}
		expected[header.Name] = true
	}
	for name, found := range expected {
		if !found {
			return fmt.Errorf("Control 更新 tar 缺少主程序: %s", name)
		}
	}
	return nil
}

func validateTarTopLevel(path, topLevel, required string, singleFile bool) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	compressed, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("打开更新 gzip 失败: %w", err)
	}
	defer compressed.Close()
	reader := tar.NewReader(compressed)
	entries := 0
	foundRequired := false
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("读取更新 tar 失败: %w", err)
		}
		entries++
		clean := filepath.ToSlash(filepath.Clean(header.Name))
		if clean == "." || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/") || (clean != topLevel && !strings.HasPrefix(clean, topLevel+"/")) {
			return fmt.Errorf("更新 tar 包含非法顶层条目: %s", header.Name)
		}
		if clean == required && header.Typeflag == tar.TypeReg {
			foundRequired = true
		}
	}
	if !foundRequired {
		return fmt.Errorf("更新 tar 缺少主程序: %s", required)
	}
	if singleFile && entries != 1 {
		return errors.New("Agent 更新 tar 必须只含单个 homestack-agent 文件")
	}
	return nil
}

func validateAssetSet(names []string, version string) {
	expected := []string{}
	for _, arch := range []string{"amd64", "arm64"} {
		expected = append(expected,
			"homestack-control_"+version+"_linux_"+arch+".tar.gz",
			"homestack-control-update_"+version+"_linux_"+arch+".tar.gz",
			"homestack-agent_"+version+"_linux_"+arch+".tar.gz",
			"homestack-agent-update_"+version+"_linux_"+arch+".tar.gz",
			"HomeStack_"+version+"_darwin_"+arch+".dmg",
			"HomeStack_"+version+"_darwin_"+arch+"_update.tar.gz",
			"HomeStack_"+version+"_windows_"+arch+"_setup.exe",
			"HomeStack_"+version+"_windows_"+arch+"_portable.zip",
			"HomeStack_"+version+"_windows_"+arch+"_update.zip",
			"HomeStack_"+version+"_linux_"+arch+".AppImage",
			"HomeStack_"+version+"_linux_"+arch+".deb",
		)
	}
	sort.Strings(expected)
	if strings.Join(names, "\n") != strings.Join(expected, "\n") {
		fatal(fmt.Errorf("发布资产集合不完整或包含未知文件\n期望:\n%s\n实际:\n%s", strings.Join(expected, "\n"), strings.Join(names, "\n")))
	}
}

func manifestArtifact(name, version, repository, tag string, size int64, digest, signature []byte) (artifact, bool) {
	current := artifact{URL: "https://github.com/" + repository + "/releases/download/" + tag + "/" + name, Filename: name, Size: size, DigestAlgo: "sha256", Digest: base64.StdEncoding.EncodeToString(digest), SignatureAlgo: "ed25519", Signature: base64.StdEncoding.EncodeToString(signature)}
	for _, platform := range []string{"darwin", "windows", "linux"} {
		for _, arch := range []string{"amd64", "arm64"} {
			var expected, filetype string
			switch platform {
			case "darwin":
				expected, filetype = "HomeStack_"+version+"_darwin_"+arch+"_update.tar.gz", "tar.gz"
			case "windows":
				expected, filetype = "HomeStack_"+version+"_windows_"+arch+"_update.zip", "zip"
			case "linux":
				expected, filetype = "HomeStack_"+version+"_linux_"+arch+".AppImage", "appimage"
			}
			if name == expected {
				current.Component, current.Platform, current.Arch, current.Filetype = "desktop", platform, arch, filetype
				return current, true
			}
		}
	}
	for _, arch := range []string{"amd64", "arm64"} {
		if name == "homestack-control-update_"+version+"_linux_"+arch+".tar.gz" {
			current.Component, current.Platform, current.Arch, current.Filetype = "control", "linux", arch, "tar.gz"
			return current, true
		}
		if name == "homestack-agent-update_"+version+"_linux_"+arch+".tar.gz" {
			current.Component, current.Platform, current.Arch, current.Filetype = "agent", "linux", arch, "tar.gz"
			return current, true
		}
	}
	return artifact{}, false
}

func isInstallerAsset(name, version string) bool {
	for _, arch := range []string{"amd64", "arm64"} {
		if name == "homestack-control_"+version+"_linux_"+arch+".tar.gz" || name == "homestack-agent_"+version+"_linux_"+arch+".tar.gz" {
			return true
		}
	}
	return false
}

func writeDetachedSignature(path string, signature []byte) {
	if err := os.WriteFile(path+".sig", []byte(base64.StdEncoding.EncodeToString(signature)+"\n"), 0o644); err != nil {
		fatal(err)
	}
}

func decodeKey(raw string, size int, label string) []byte {
	for _, encoding := range []*base64.Encoding{base64.RawStdEncoding, base64.StdEncoding, base64.RawURLEncoding, base64.URLEncoding} {
		if decoded, err := encoding.DecodeString(strings.TrimSpace(raw)); err == nil && len(decoded) == size {
			return decoded
		}
	}
	fatal(fmt.Errorf("Ed25519 %s编码或长度无效", label))
	return nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "错误:", err)
	os.Exit(1)
}
