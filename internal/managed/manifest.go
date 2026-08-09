package managed

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"runtime"
	"strings"
)

const ManifestSchema = 1

type Artifact struct {
	Component string `json:"component"`
	Version   string `json:"version"`
	Platform  string `json:"platform"`
	Arch      string `json:"arch"`
	URL       string `json:"url"`
	Filename  string `json:"filename"`
	Format    string `json:"format"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256"`
}

type Manifest struct {
	SchemaVersion int        `json:"schema_version"`
	Artifacts     []Artifact `json:"artifacts"`
}

type Profile struct {
	StateDir         string                       `json:"state_dir"`
	FileRoot         string                       `json:"file_root"`
	FileBrowser      Installation                 `json:"filebrowser"`
	Jellyfin         Installation                 `json:"jellyfin"`
	JellyfinPassword string                       `json:"jellyfin_password"`
	ModuleSecrets    map[string]map[string]string `json:"module_secrets,omitempty"`
}

type signedManifest struct {
	Payload   json.RawMessage `json:"payload"`
	Signature string          `json:"signature"`
}

func SignManifest(manifest Manifest, privateKey ed25519.PrivateKey) ([]byte, error) {
	if len(privateKey) != ed25519.PrivateKeySize {
		return nil, errors.New("组件清单签名私钥无效")
	}
	if err := ValidateManifest(manifest); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(manifest)
	if err != nil {
		return nil, err
	}
	envelope := signedManifest{Payload: payload, Signature: base64.RawURLEncoding.EncodeToString(ed25519.Sign(privateKey, payload))}
	data, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func FetchManifest(ctx context.Context, client *http.Client, endpoint string, publicKey ed25519.PublicKey) (Manifest, error) {
	if client == nil || len(publicKey) != ed25519.PublicKeySize {
		return Manifest{}, errors.New("组件清单客户端或签名公钥无效")
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil {
		return Manifest{}, errors.New("组件清单地址必须是无凭据 HTTPS URL")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Manifest{}, err
	}
	response, err := client.Do(request)
	if err != nil {
		return Manifest{}, fmt.Errorf("下载组件清单失败: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Manifest{}, fmt.Errorf("下载组件清单失败: HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return Manifest{}, fmt.Errorf("读取组件清单失败: %w", err)
	}
	var envelope signedManifest
	if err := json.Unmarshal(data, &envelope); err != nil {
		return Manifest{}, fmt.Errorf("解析组件清单失败: %w", err)
	}
	signature, err := base64.RawURLEncoding.DecodeString(envelope.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize || !ed25519.Verify(publicKey, envelope.Payload, signature) {
		return Manifest{}, errors.New("组件清单 Ed25519 签名无效")
	}
	var manifest Manifest
	if err := json.Unmarshal(envelope.Payload, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("解析已签名组件清单失败: %w", err)
	}
	if err := ValidateManifest(manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func ValidateManifest(manifest Manifest) error {
	if manifest.SchemaVersion != ManifestSchema {
		return fmt.Errorf("组件清单 schema 不受支持: %d", manifest.SchemaVersion)
	}
	seen := make(map[string]struct{}, len(manifest.Artifacts))
	for _, artifact := range manifest.Artifacts {
		if !contains([]string{"filebrowser", "jellyfin"}, artifact.Component) || artifact.Version == "" {
			return errors.New("组件清单包含未知组件或空版本")
		}
		if !contains([]string{"darwin", "windows", "linux"}, artifact.Platform) || !contains([]string{"amd64", "arm64"}, artifact.Arch) {
			return errors.New("组件清单包含不支持的平台或架构")
		}
		digest, digestErr := hex.DecodeString(artifact.SHA256)
		if !contains([]string{"binary", "zip", "tar.gz", "tar.xz"}, artifact.Format) || artifact.Size < 1 || artifact.Size > 512<<20 || digestErr != nil || len(digest) != sha256.Size {
			return errors.New("组件清单资产格式、大小或 SHA-256 无效")
		}
		parsed, err := url.Parse(artifact.URL)
		if err != nil || parsed.Scheme != "https" || parsed.User != nil || !contains([]string{"github.com", "repo.jellyfin.org"}, strings.ToLower(parsed.Hostname())) {
			return errors.New("组件资产必须来自批准的无凭据 HTTPS 主机")
		}
		if artifact.Filename == "" || strings.ContainsAny(artifact.Filename, `/\\`) {
			return errors.New("组件资产文件名无效")
		}
		key := artifact.Component + "/" + artifact.Platform + "/" + artifact.Arch
		if _, exists := seen[key]; exists {
			return fmt.Errorf("组件清单资产重复: %s", key)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func (m Manifest) Current(component string) (Artifact, error) {
	for _, artifact := range m.Artifacts {
		if artifact.Component == component && artifact.Platform == runtime.GOOS && artifact.Arch == runtime.GOARCH {
			return artifact, nil
		}
	}
	return Artifact{}, fmt.Errorf("组件 %s 缺少 %s/%s 资产", component, runtime.GOOS, runtime.GOARCH)
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
