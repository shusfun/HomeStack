package managed

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/wangshangbin/homestack/internal/protocol"
)

const (
	FileBrowserURL = "http://127.0.0.1:19445"
	JellyfinURL    = "http://127.0.0.1:19446"

	jellyfinRequestTimeout = 20 * time.Second
	jellyfinLibraryTimeout = 2 * time.Minute
)

type Runtime struct {
	commands []*exec.Cmd
}

func Start(ctx context.Context, profile *Profile, directories []protocol.SharedDirectory) (*Runtime, error) {
	if profile == nil {
		return nil, errors.New("缺少托管内容档案")
	}
	if err := ValidateProfile(*profile); err != nil {
		return nil, err
	}
	if len(directories) == 0 {
		return nil, errors.New("托管内容没有共享目录")
	}
	for _, name := range []string{"filebrowser/root", "jellyfin/config", "jellyfin/data", "jellyfin/cache", "jellyfin/log"} {
		if err := os.MkdirAll(filepath.Join(profile.StateDir, name), 0o700); err != nil {
			return nil, err
		}
	}
	if err := writeJellyfinNetworkConfig(filepath.Join(profile.StateDir, "jellyfin", "config", "network.xml")); err != nil {
		return nil, err
	}
	fileConfig := filepath.Join(profile.StateDir, "filebrowser", "config.yaml")
	if err := writeFileBrowserConfig(fileConfig, directories, profile); err != nil {
		return nil, err
	}
	runtime := &Runtime{}
	fileCommand, err := startProcess(ctx, profile.FileBrowser.Executable, []string{"-c", fileConfig}, nil, filepath.Join(profile.StateDir, "filebrowser", "filebrowser.log"))
	if err != nil {
		return nil, err
	}
	runtime.commands = append(runtime.commands, fileCommand)
	mediaArgs := []string{
		"--service", "--datadir", filepath.Join(profile.StateDir, "jellyfin/data"),
		"--cachedir", filepath.Join(profile.StateDir, "jellyfin/cache"), "--configdir", filepath.Join(profile.StateDir, "jellyfin/config"),
		"--logdir", filepath.Join(profile.StateDir, "jellyfin/log"), "--webdir", profile.Jellyfin.WebDir,
		"--published-server-url", JellyfinURL, "--nonetchange",
	}
	mediaArgs = append(mediaArgs, "--ffmpeg", profile.FFmpeg.Executable)
	mediaEnv := []string{"ASPNETCORE_URLS=" + JellyfinURL, "DOTNET_CLI_TELEMETRY_OPTOUT=1"}
	mediaCommand, err := startProcess(ctx, profile.Jellyfin.Executable, mediaArgs, mediaEnv, filepath.Join(profile.StateDir, "jellyfin", "jellyfin.log"))
	if err != nil {
		runtime.stop()
		return nil, err
	}
	runtime.commands = append(runtime.commands, mediaCommand)
	client := &http.Client{Timeout: jellyfinRequestTimeout}
	if err := waitForHealth(ctx, client, FileBrowserURL+"/health", "FileBrowser"); err != nil {
		runtime.stop()
		return nil, err
	}
	if err := waitForHealth(ctx, client, JellyfinURL+"/System/Info/Public", "Jellyfin"); err != nil {
		runtime.stop()
		return nil, err
	}
	startupConfigurationReady, err := waitForJellyfinStartupConfiguration(ctx, client, JellyfinURL, 500*time.Millisecond, 2*time.Minute)
	if err != nil {
		runtime.stop()
		return nil, err
	}
	existingAPIKey := ""
	if values := profile.ModuleSecrets["jellyfin"]; values != nil {
		existingAPIKey = values["api_key"]
	}
	if !startupConfigurationReady && existingAPIKey == "" {
		existingAPIKey, err = recoverJellyfinAPIKey(ctx, profile.StateDir)
		if err != nil {
			runtime.stop()
			return nil, err
		}
	}
	token, err := configureJellyfin(ctx, client, profile.JellyfinPassword, existingAPIKey, directories, startupConfigurationReady)
	if err != nil {
		runtime.stop()
		return nil, err
	}
	if profile.ModuleSecrets == nil {
		profile.ModuleSecrets = make(map[string]map[string]string)
	}
	profile.ModuleSecrets["jellyfin"] = map[string]string{"api_key": token}
	return runtime, nil
}

type jellyfinXMLStrings struct {
	Values []string `xml:"string"`
}

type jellyfinNetworkConfiguration struct {
	XMLName                           xml.Name           `xml:"NetworkConfiguration"`
	BaseURL                           string             `xml:"BaseUrl"`
	EnableHTTPS                       bool               `xml:"EnableHttps"`
	RequireHTTPS                      bool               `xml:"RequireHttps"`
	CertificatePath                   string             `xml:"CertificatePath"`
	CertificatePassword               string             `xml:"CertificatePassword"`
	InternalHTTPPort                  int                `xml:"InternalHttpPort"`
	InternalHTTPSPort                 int                `xml:"InternalHttpsPort"`
	PublicHTTPPort                    int                `xml:"PublicHttpPort"`
	PublicHTTPSPort                   int                `xml:"PublicHttpsPort"`
	AutoDiscovery                     bool               `xml:"AutoDiscovery"`
	EnableIPv4                        bool               `xml:"EnableIPv4"`
	EnableIPv6                        bool               `xml:"EnableIPv6"`
	EnableRemoteAccess                bool               `xml:"EnableRemoteAccess"`
	LocalNetworkSubnets               jellyfinXMLStrings `xml:"LocalNetworkSubnets"`
	LocalNetworkAddresses             jellyfinXMLStrings `xml:"LocalNetworkAddresses"`
	KnownProxies                      jellyfinXMLStrings `xml:"KnownProxies"`
	IgnoreVirtualInterfaces           bool               `xml:"IgnoreVirtualInterfaces"`
	VirtualInterfaceNames             jellyfinXMLStrings `xml:"VirtualInterfaceNames"`
	EnablePublishedServerURIByRequest bool               `xml:"EnablePublishedServerUriByRequest"`
	PublishedServerURIBySubnet        jellyfinXMLStrings `xml:"PublishedServerUriBySubnet"`
	RemoteIPFilter                    jellyfinXMLStrings `xml:"RemoteIPFilter"`
	IsRemoteIPFilterBlacklist         bool               `xml:"IsRemoteIPFilterBlacklist"`
}

func writeJellyfinNetworkConfig(path string) error {
	configuration := jellyfinNetworkConfiguration{
		InternalHTTPPort:        19446,
		InternalHTTPSPort:       8920,
		PublicHTTPPort:          19446,
		PublicHTTPSPort:         8920,
		EnableIPv4:              true,
		EnableRemoteAccess:      false,
		LocalNetworkAddresses:   jellyfinXMLStrings{Values: []string{"127.0.0.1"}},
		IgnoreVirtualInterfaces: true,
		VirtualInterfaceNames:   jellyfinXMLStrings{Values: []string{"veth"}},
	}
	data, err := xml.MarshalIndent(configuration, "", "  ")
	if err != nil {
		return fmt.Errorf("生成 Jellyfin 网络配置失败: %w", err)
	}
	data = append([]byte(xml.Header), data...)
	data = append(data, '\n')
	if err := atomicFile(path, data, 0o600); err != nil {
		return fmt.Errorf("写入 Jellyfin 网络配置失败: %w", err)
	}
	return nil
}

func (r *Runtime) stop() {
	for _, command := range r.commands {
		if command.Process != nil {
			_ = command.Process.Kill()
		}
	}
}

func writeFileBrowserConfig(path string, directories []protocol.SharedDirectory, profile *Profile) error {
	if profile == nil {
		return errors.New("缺少 FileBrowser 托管档案")
	}
	sources := make([]map[string]any, 0, len(directories))
	names := make(map[string]struct{}, len(directories))
	paths := make(map[string]struct{}, len(directories))
	for _, directory := range directories {
		name := strings.TrimSpace(directory.Name)
		cleanPath := filepath.Clean(directory.Path)
		if name == "" || directory.ID == "" || !filepath.IsAbs(cleanPath) {
			return fmt.Errorf("FileBrowser 共享目录无效: id=%q name=%q", directory.ID, directory.Name)
		}
		if _, exists := names[name]; exists {
			return fmt.Errorf("FileBrowser 共享目录名称重复: %s", name)
		}
		if _, exists := paths[cleanPath]; exists {
			return fmt.Errorf("FileBrowser 共享目录路径重复: %s", cleanPath)
		}
		names[name] = struct{}{}
		paths[cleanPath] = struct{}{}
		sources = append(sources, map[string]any{
			"path": cleanPath,
			"name": name,
			"config": map[string]any{
				"readOnly":       true,
				"private":        true,
				"defaultEnabled": true,
				"rules": []map[string]any{{
					"folderPath":     "/",
					"ignoreHidden":   true,
					"ignoreSymlinks": true,
				}},
			},
		})
	}
	if len(sources) == 0 {
		return errors.New("FileBrowser 没有可用的共享目录")
	}
	config := map[string]any{
		"server": map[string]any{
			"listen":             "127.0.0.1",
			"port":               19445,
			"baseURL":            "/",
			"database":           filepath.Join(profile.StateDir, "filebrowser", "database-"+FileBrowserVersion+".db"),
			"cacheDir":           filepath.Join(profile.StateDir, "filebrowser", "cache-"+FileBrowserVersion),
			"disableUpdateCheck": true,
			"disableWebDAV":      true,
			"logging": []map[string]any{{
				"levels":   "info|warning|error",
				"output":   "stdout",
				"noColors": true,
			}},
			"sources": sources,
		},
		"auth": map[string]any{
			"methods": map[string]any{"noauth": true},
		},
		"frontend": map[string]any{
			"name":                  "HomeStack",
			"disableDefaultLinks":   true,
			"disableUsedPercentage": true,
		},
		"userDefaults": map[string]any{
			"listing": map[string]any{"showHidden": false},
			"account": map[string]any{
				"lockPassword":    true,
				"disableSettings": true,
				"permissions": map[string]any{
					"api": false, "admin": false, "modify": false, "share": false,
					"realtime": false, "delete": false, "create": false, "download": true,
				},
			},
		},
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("生成 FileBrowser 配置失败: %w", err)
	}
	data = append(data, '\n')
	return atomicFile(path, data, 0o600)
}

func startProcess(ctx context.Context, executable string, args, extraEnv []string, logPath string) (*exec.Cmd, error) {
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	command := exec.CommandContext(ctx, executable, args...)
	command.Env = append(os.Environ(), extraEnv...)
	command.Stdout, command.Stderr = logFile, logFile
	if err := command.Start(); err != nil {
		_ = logFile.Close()
		return nil, fmt.Errorf("启动 %s 失败: %w", filepath.Base(executable), err)
	}
	go func() {
		_ = command.Wait()
		_ = logFile.Close()
	}()
	return command, nil
}

func waitForHealth(ctx context.Context, client *http.Client, endpoint, name string) error {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.NewTimer(2 * time.Minute)
	defer timeout.Stop()
	for {
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		response, err := client.Do(request)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode >= 200 && response.StatusCode < 300 {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timeout.C:
			return fmt.Errorf("%s 启动后两分钟内未通过健康检查", name)
		case <-ticker.C:
		}
	}
}

func waitForJellyfinStartupConfiguration(ctx context.Context, client *http.Client, baseURL string, retryInterval, startupTimeout time.Duration) (bool, error) {
	ticker := time.NewTicker(retryInterval)
	defer ticker.Stop()
	timeout := time.NewTimer(startupTimeout)
	defer timeout.Stop()
	lastUnavailable := ""
	sawServiceUnavailable := false
	waitForRetry := func(detail string) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timeout.C:
			return fmt.Errorf("Jellyfin 启动后两分钟内未准备好 /Startup/Configuration: %s", detail)
		case <-ticker.C:
			return nil
		}
	}
	for {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/Startup/Configuration", nil)
		if err != nil {
			return false, fmt.Errorf("创建 Jellyfin /Startup/Configuration 请求失败: %w", err)
		}
		response, err := client.Do(request)
		if err != nil {
			if !sawServiceUnavailable {
				return false, fmt.Errorf("请求 Jellyfin /Startup/Configuration 失败: %w", err)
			}
			lastUnavailable = err.Error()
			if err := waitForRetry(lastUnavailable); err != nil {
				return false, err
			}
			continue
		}
		if response.StatusCode == http.StatusOK {
			var configuration map[string]any
			decodeErr := json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&configuration)
			_ = response.Body.Close()
			if decodeErr != nil {
				return false, fmt.Errorf("解析 Jellyfin /Startup/Configuration 响应失败: %w", decodeErr)
			}
			return true, nil
		}
		detail, _ := io.ReadAll(io.LimitReader(response.Body, 32<<10))
		_ = response.Body.Close()
		if response.StatusCode == http.StatusUnauthorized {
			request, requestErr := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/System/Info/Public", nil)
			if requestErr != nil {
				return false, fmt.Errorf("创建 Jellyfin /System/Info/Public 请求失败: %w", requestErr)
			}
			infoResponse, requestErr := client.Do(request)
			if requestErr != nil {
				sawServiceUnavailable = true
				lastUnavailable = requestErr.Error()
				if err := waitForRetry(lastUnavailable); err != nil {
					return false, err
				}
				continue
			}
			if infoResponse.StatusCode == http.StatusServiceUnavailable {
				sawServiceUnavailable = true
				detail, _ := io.ReadAll(io.LimitReader(infoResponse.Body, 32<<10))
				_ = infoResponse.Body.Close()
				lastUnavailable = strings.TrimSpace(string(detail))
				if err := waitForRetry(lastUnavailable); err != nil {
					return false, err
				}
				continue
			}
			if infoResponse.StatusCode != http.StatusOK {
				detail, _ := io.ReadAll(io.LimitReader(infoResponse.Body, 32<<10))
				_ = infoResponse.Body.Close()
				return false, fmt.Errorf("Jellyfin /System/Info/Public 返回 HTTP %d: %s", infoResponse.StatusCode, strings.TrimSpace(string(detail)))
			}
			var info struct {
				StartupWizardCompleted bool `json:"StartupWizardCompleted"`
			}
			decodeErr := json.NewDecoder(io.LimitReader(infoResponse.Body, 4<<20)).Decode(&info)
			_ = infoResponse.Body.Close()
			if decodeErr != nil {
				return false, fmt.Errorf("解析 Jellyfin /System/Info/Public 响应失败: %w", decodeErr)
			}
			if info.StartupWizardCompleted {
				return false, nil
			}
			return false, fmt.Errorf("Jellyfin /Startup/Configuration 返回 HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(detail)))
		}
		if response.StatusCode != http.StatusServiceUnavailable {
			return false, fmt.Errorf("Jellyfin /Startup/Configuration 返回 HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(detail)))
		}
		sawServiceUnavailable = true
		lastUnavailable = strings.TrimSpace(string(detail))
		if err := waitForRetry(lastUnavailable); err != nil {
			return false, err
		}
	}
}

func configureJellyfin(ctx context.Context, client *http.Client, password, existingAPIKey string, directories []protocol.SharedDirectory, startupConfigurationReady bool) (string, error) {
	accessToken, apiKey := existingAPIKey, existingAPIKey
	if startupConfigurationReady {
		if err := configureJellyfinStartup(ctx, client, JellyfinURL, password); err != nil {
			return "", err
		}
		var auth struct {
			AccessToken string `json:"AccessToken"`
		}
		if err := jellyfinJSON(ctx, client, http.MethodPost, "/Users/AuthenticateByName", "", map[string]string{"Username": "homestack", "Pw": password}, &auth, http.StatusOK); err != nil {
			return "", err
		}
		if auth.AccessToken == "" {
			return "", errors.New("Jellyfin 登录响应缺少访问令牌")
		}
		accessToken = auth.AccessToken
		var err error
		apiKey, err = ensureJellyfinAPIKey(ctx, client, accessToken)
		if err != nil {
			return "", err
		}
	} else if existingAPIKey == "" {
		return "", errors.New("已配置的 Jellyfin 缺少 HomeStack API Key")
	}
	var folders []struct {
		Name      string   `json:"Name"`
		Locations []string `json:"Locations"`
	}
	if err := jellyfinJSON(ctx, client, http.MethodGet, "/Library/VirtualFolders", accessToken, nil, &folders, http.StatusOK); err != nil {
		return "", err
	}
	libraryClient := *client
	libraryClient.Timeout = jellyfinLibraryTimeout
	found := false
	currentPaths := []string{}
	for _, folder := range folders {
		if folder.Name == "HomeStack" {
			found = true
			currentPaths = folder.Locations
		}
	}
	if !found {
		pathInfos := make([]map[string]string, 0, len(directories))
		for _, directory := range directories {
			pathInfos = append(pathInfos, map[string]string{"Path": directory.Path})
		}
		query := url.Values{"name": {"HomeStack"}, "collectionType": {"mixed"}, "refreshLibrary": {"true"}}
		body := map[string]any{"LibraryOptions": map[string]any{"PathInfos": pathInfos}}
		if err := jellyfinJSON(ctx, &libraryClient, http.MethodPost, "/Library/VirtualFolders?"+query.Encode(), accessToken, body, nil, http.StatusNoContent); err != nil {
			return "", err
		}
	} else if err := reconcileJellyfinPaths(ctx, &libraryClient, accessToken, currentPaths, directories); err != nil {
		return "", err
	}
	return apiKey, nil
}

func configureJellyfinStartup(ctx context.Context, client *http.Client, baseURL, password string) error {
	if err := jellyfinJSONAt(ctx, client, baseURL, http.MethodPost, "/Startup/Configuration", "", map[string]any{"ServerName": "HomeStack", "UICulture": "zh-CN", "MetadataCountryCode": "CN", "PreferredMetadataLanguage": "zh-CN"}, nil, http.StatusNoContent); err != nil {
		return err
	}
	var firstUser map[string]any
	if err := jellyfinJSONAt(ctx, client, baseURL, http.MethodGet, "/Startup/User", "", nil, &firstUser, http.StatusOK); err != nil {
		return err
	}
	steps := []struct {
		path string
		body any
	}{
		{"/Startup/User", map[string]any{"Name": "homestack", "Password": password}},
		{"/Startup/RemoteAccess", map[string]any{"EnableRemoteAccess": false, "EnableAutomaticPortMapping": false}},
		{"/Startup/Complete", map[string]any{}},
	}
	for _, step := range steps {
		if err := jellyfinJSONAt(ctx, client, baseURL, http.MethodPost, step.path, "", step.body, nil, http.StatusNoContent); err != nil {
			return err
		}
	}
	return nil
}

func reconcileJellyfinPaths(ctx context.Context, client *http.Client, accessToken string, current []string, directories []protocol.SharedDirectory) error {
	existing := make(map[string]struct{}, len(current))
	for _, path := range current {
		existing[filepath.Clean(path)] = struct{}{}
	}
	desired := make(map[string]struct{}, len(directories))
	for _, directory := range directories {
		path := filepath.Clean(directory.Path)
		desired[path] = struct{}{}
		if _, ok := existing[path]; ok {
			continue
		}
		query := url.Values{"refreshLibrary": {"true"}}
		body := map[string]any{"Name": "HomeStack", "PathInfo": map[string]string{"Path": path}}
		if err := jellyfinJSON(ctx, client, http.MethodPost, "/Library/VirtualFolders/Paths?"+query.Encode(), accessToken, body, nil, http.StatusNoContent); err != nil {
			return err
		}
	}
	for path := range existing {
		if _, ok := desired[path]; ok {
			continue
		}
		query := url.Values{"name": {"HomeStack"}, "path": {path}, "refreshLibrary": {"true"}}
		if err := jellyfinJSON(ctx, client, http.MethodDelete, "/Library/VirtualFolders/Paths?"+query.Encode(), accessToken, nil, nil, http.StatusNoContent); err != nil {
			return err
		}
	}
	return nil
}

func ensureJellyfinAPIKey(ctx context.Context, client *http.Client, accessToken string) (string, error) {
	type keyInfo struct {
		AppName     string `json:"AppName"`
		AccessToken string `json:"AccessToken"`
	}
	load := func() ([]keyInfo, error) {
		var result struct {
			Items []keyInfo `json:"Items"`
		}
		if err := jellyfinJSON(ctx, client, http.MethodGet, "/Auth/Keys", accessToken, nil, &result, http.StatusOK); err != nil {
			return nil, err
		}
		return result.Items, nil
	}
	keys, err := load()
	if err != nil {
		return "", err
	}
	for _, key := range keys {
		if key.AppName == "HomeStack" && key.AccessToken != "" {
			return key.AccessToken, nil
		}
	}
	if err := jellyfinJSON(ctx, client, http.MethodPost, "/Auth/Keys?app=HomeStack", accessToken, nil, nil, http.StatusNoContent); err != nil {
		return "", err
	}
	keys, err = load()
	if err != nil {
		return "", err
	}
	for _, key := range keys {
		if key.AppName == "HomeStack" && key.AccessToken != "" {
			return key.AccessToken, nil
		}
	}
	return "", errors.New("Jellyfin 创建 API Key 后未返回 HomeStack 凭据")
}

func jellyfinJSON(ctx context.Context, client *http.Client, method, path, token string, body, target any, expected int) error {
	return jellyfinJSONAt(ctx, client, JellyfinURL, method, path, token, body, target, expected)
}

func jellyfinJSONAt(ctx context.Context, client *http.Client, baseURL, method, path, token string, body, target any, expected int) error {
	var input io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		input = bytes.NewReader(data)
	}
	request, err := http.NewRequestWithContext(ctx, method, baseURL+path, input)
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Emby-Authorization", `MediaBrowser Client="HomeStack", Device="HomeStack Node", DeviceId="homestack-node", Version="1"`)
	if token != "" {
		request.Header.Set("X-Emby-Token", token)
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("请求 Jellyfin %s 失败: %w", path, err)
	}
	defer response.Body.Close()
	if response.StatusCode != expected {
		detail, _ := io.ReadAll(io.LimitReader(response.Body, 32<<10))
		return fmt.Errorf("Jellyfin %s 返回 HTTP %d: %s", path, response.StatusCode, strings.TrimSpace(string(detail)))
	}
	if target != nil {
		if err := json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(target); err != nil {
			return fmt.Errorf("解析 Jellyfin %s 响应失败: %w", path, err)
		}
	}
	return nil
}

func atomicFile(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".homestack-*")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if err := file.Chmod(mode); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}
