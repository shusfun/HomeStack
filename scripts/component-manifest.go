//go:build ignore

package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/wangshangbin/homestack/internal/managed"
)

func main() {
	output := flag.String("output", "dist/components.json", "组件清单输出路径")
	lockPath := flag.String("lock", "scripts/components.lock.json", "组件锁定清单路径")
	probeKey := flag.String("print-probe", "", "输出指定 component/platform/arch 的官方 URL 和大小")
	privateEncoded := flag.String("private-key", "", "base64 Ed25519 私钥")
	flag.Parse()
	artifacts, err := readComponentLock(*lockPath)
	if err != nil {
		fatal(err)
	}
	if *probeKey != "" {
		probeURL, probeSize, err := componentProbe(artifacts, *probeKey)
		if err != nil {
			fatal(err)
		}
		fmt.Printf("%s\t%d\n", probeURL, probeSize)
		return
	}
	privateKey := decodeKey(*privateEncoded)
	if len(privateKey) != ed25519.PrivateKeySize {
		fatal(errors.New("组件清单需要有效的 base64 Ed25519 私钥"))
	}
	for index := range artifacts {
		artifacts[index].URLs = candidateURLs(artifacts[index].URL)
	}
	data, err := managed.SignManifest(managed.Manifest{SchemaVersion: managed.ManifestSchema, Artifacts: artifacts}, ed25519.PrivateKey(privateKey))
	if err != nil {
		fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(*output), 0o755); err != nil {
		fatal(err)
	}
	if err := os.WriteFile(*output, data, 0o644); err != nil {
		fatal(err)
	}
}

func componentProbe(artifacts []managed.Artifact, key string) (string, int64, error) {
	for _, artifact := range artifacts {
		current := artifact.Component + "/" + artifact.Platform + "/" + artifact.Arch
		if current == key {
			return artifact.URL, artifact.Size, nil
		}
	}
	return "", 0, fmt.Errorf("组件锁定清单缺少测速资产: %s", key)
}

func readComponentLock(path string) ([]managed.Artifact, error) {
	lockData, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var locked struct {
		SchemaVersion int                `json:"schema_version"`
		Artifacts     []managed.Artifact `json:"artifacts"`
	}
	if err := json.Unmarshal(lockData, &locked); err != nil {
		return nil, fmt.Errorf("解析组件锁定清单失败: %w", err)
	}
	if locked.SchemaVersion != 1 || len(locked.Artifacts) != 18 {
		return nil, fmt.Errorf("组件锁定清单不完整: schema=%d artifacts=%d", locked.SchemaVersion, len(locked.Artifacts))
	}
	return locked.Artifacts, nil
}

func candidateURLs(official string) []string {
	if !strings.HasPrefix(official, "https://github.com/") {
		return []string{official}
	}
	return []string{
		"https://ghproxy.net/" + official,
		"https://ghfast.top/" + official,
		"https://gh-proxy.com/" + official,
		official,
	}
}

func decodeKey(raw string) []byte {
	for _, encoding := range []*base64.Encoding{base64.RawStdEncoding, base64.StdEncoding, base64.RawURLEncoding, base64.URLEncoding} {
		decoded, err := encoding.DecodeString(strings.TrimSpace(raw))
		if err == nil && len(decoded) == ed25519.PrivateKeySize {
			return decoded
		}
	}
	return nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
