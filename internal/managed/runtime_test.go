package managed

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/wangshangbin/homestack/internal/protocol"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestWriteJellyfinNetworkConfigRestrictsToLoopback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "network.xml")
	if err := os.WriteFile(path, []byte("<NetworkConfiguration><LocalNetworkAddresses /></NetworkConfiguration>"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeJellyfinNetworkConfig(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var configuration jellyfinNetworkConfiguration
	if err := xml.Unmarshal(data, &configuration); err != nil {
		t.Fatal(err)
	}
	if configuration.InternalHTTPPort != 19446 || configuration.PublicHTTPPort != 19446 {
		t.Fatalf("Jellyfin 端口未固定: internal=%d public=%d", configuration.InternalHTTPPort, configuration.PublicHTTPPort)
	}
	if !configuration.EnableIPv4 || configuration.EnableIPv6 || configuration.EnableRemoteAccess {
		t.Fatalf("Jellyfin 网络开关错误: %+v", configuration)
	}
	if len(configuration.LocalNetworkAddresses.Values) != 1 || configuration.LocalNetworkAddresses.Values[0] != "127.0.0.1" {
		t.Fatalf("Jellyfin 未限制回环监听: %v", configuration.LocalNetworkAddresses.Values)
	}
}

func TestWriteFileBrowserConfigUsesLoopbackReadOnlySources(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "managed")
	path := filepath.Join(stateDir, "filebrowser", "config.yaml")
	profile := &Profile{StateDir: stateDir, JellyfinPassword: "不得写入文件配置"}
	directories := []protocol.SharedDirectory{
		{ID: "documents", Name: "文稿", Path: filepath.Join(t.TempDir(), "Documents")},
		{ID: "videos", Name: "影视", Path: filepath.Join(t.TempDir(), "Movies")},
	}
	if err := writeFileBrowserConfig(path, directories, profile); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), profile.JellyfinPassword) {
		t.Fatal("FileBrowser 配置不应包含 Jellyfin 凭据")
	}
	var config struct {
		Server struct {
			Listen             string `json:"listen"`
			Port               int    `json:"port"`
			Database           string `json:"database"`
			CacheDir           string `json:"cacheDir"`
			DisableUpdateCheck bool   `json:"disableUpdateCheck"`
			DisableWebDAV      bool   `json:"disableWebDAV"`
			Sources            []struct {
				Path   string `json:"path"`
				Name   string `json:"name"`
				Config struct {
					ReadOnly       bool `json:"readOnly"`
					Private        bool `json:"private"`
					DefaultEnabled bool `json:"defaultEnabled"`
					Rules          []struct {
						FolderPath     string `json:"folderPath"`
						IgnoreHidden   bool   `json:"ignoreHidden"`
						IgnoreSymlinks bool   `json:"ignoreSymlinks"`
					} `json:"rules"`
				} `json:"config"`
			} `json:"sources"`
		} `json:"server"`
		Auth struct {
			Methods struct {
				NoAuth bool `json:"noauth"`
			} `json:"methods"`
		} `json:"auth"`
		UserDefaults struct {
			Account struct {
				DisableSettings bool `json:"disableSettings"`
				Permissions     struct {
					Create   bool `json:"create"`
					Modify   bool `json:"modify"`
					Delete   bool `json:"delete"`
					Share    bool `json:"share"`
					Download bool `json:"download"`
				} `json:"permissions"`
			} `json:"account"`
		} `json:"userDefaults"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("FileBrowser 配置不是合法 JSON/YAML: %v", err)
	}
	if config.Server.Listen != "127.0.0.1" || config.Server.Port != 19445 {
		t.Fatalf("FileBrowser 未限制回环监听: %s:%d", config.Server.Listen, config.Server.Port)
	}
	if !config.Server.DisableUpdateCheck || !config.Server.DisableWebDAV || !strings.Contains(config.Server.Database, FileBrowserVersion) || !strings.Contains(config.Server.CacheDir, FileBrowserVersion) {
		t.Fatalf("FileBrowser 服务配置不完整: %+v", config.Server)
	}
	if len(config.Server.Sources) != len(directories) {
		t.Fatalf("FileBrowser 来源数量错误: got=%d want=%d", len(config.Server.Sources), len(directories))
	}
	for index, source := range config.Server.Sources {
		if source.Path != directories[index].Path || source.Name != directories[index].Name || !source.Config.ReadOnly || !source.Config.Private || !source.Config.DefaultEnabled {
			t.Fatalf("FileBrowser 来源配置错误: %+v", source)
		}
		if len(source.Config.Rules) != 1 || source.Config.Rules[0].FolderPath != "/" || !source.Config.Rules[0].IgnoreHidden || !source.Config.Rules[0].IgnoreSymlinks {
			t.Fatalf("FileBrowser 来源过滤规则错误: %+v", source.Config.Rules)
		}
	}
	permissions := config.UserDefaults.Account.Permissions
	if !config.Auth.Methods.NoAuth || !config.UserDefaults.Account.DisableSettings || permissions.Create || permissions.Modify || permissions.Delete || permissions.Share || !permissions.Download {
		t.Fatalf("FileBrowser 只读权限配置错误: auth=%+v account=%+v", config.Auth, config.UserDefaults.Account)
	}
}

func TestWriteFileBrowserConfigRejectsDuplicateSourceNames(t *testing.T) {
	profile := &Profile{StateDir: t.TempDir()}
	directories := []protocol.SharedDirectory{
		{ID: "one", Name: "重复", Path: filepath.Join(t.TempDir(), "one")},
		{ID: "two", Name: "重复", Path: filepath.Join(t.TempDir(), "two")},
	}
	err := writeFileBrowserConfig(filepath.Join(t.TempDir(), "config.yaml"), directories, profile)
	if err == nil || !strings.Contains(err.Error(), "名称重复") {
		t.Fatalf("重复 FileBrowser 来源名称未返回真实错误: %v", err)
	}
}

func TestFileBrowserBinaryIntegration(t *testing.T) {
	binary := os.Getenv("HOMESTACK_FILEBROWSER_TEST_BINARY")
	if binary == "" {
		t.Skip("未提供 HOMESTACK_FILEBROWSER_TEST_BINARY")
	}
	versionOutput, err := exec.Command(binary, "version").CombinedOutput()
	if err != nil {
		t.Fatalf("读取 FileBrowser 版本失败: %v: %s", err, strings.TrimSpace(string(versionOutput)))
	}
	if !strings.Contains(string(versionOutput), "1.5.1") {
		t.Fatalf("FileBrowser 版本不匹配: %s", strings.TrimSpace(string(versionOutput)))
	}

	documents := t.TempDir()
	videos := t.TempDir()
	if err := os.WriteFile(filepath.Join(documents, "sample.txt"), []byte("homestack"), 0o600); err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(t.TempDir(), "managed")
	configPath := filepath.Join(stateDir, "filebrowser", "config.yaml")
	directories := []protocol.SharedDirectory{
		{ID: "documents", Name: "文稿", Path: documents},
		{ID: "videos", Name: "影视", Path: videos},
	}
	if err := writeFileBrowserConfig(configPath, directories, &Profile{StateDir: stateDir}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var logs bytes.Buffer
	command := exec.CommandContext(ctx, binary, "-c", configPath)
	command.Stdout = &logs
	command.Stderr = &logs
	if err := command.Start(); err != nil {
		t.Fatalf("启动 FileBrowser 失败: %v", err)
	}
	waited := make(chan error, 1)
	go func() { waited <- command.Wait() }()
	processExited := false
	defer func() {
		cancel()
		if processExited {
			return
		}
		select {
		case <-waited:
		case <-time.After(5 * time.Second):
			t.Errorf("FileBrowser 测试进程未退出")
		}
	}()

	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(30 * time.Second)
	for {
		response, requestErr := client.Get(FileBrowserURL + "/health")
		if requestErr == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				break
			}
		}
		select {
		case processErr := <-waited:
			processExited = true
			t.Fatalf("FileBrowser 启动前退出: %v: %s", processErr, logs.String())
		default:
		}
		if time.Now().After(deadline) {
			cancel()
			<-waited
			processExited = true
			t.Fatalf("FileBrowser 30 秒内未通过健康检查: %s", logs.String())
		}
		time.Sleep(100 * time.Millisecond)
	}
	if runtime.GOOS == "darwin" {
		netstatOutput, netstatErr := exec.Command("netstat", "-anv", "-p", "tcp").CombinedOutput()
		if netstatErr != nil {
			t.Fatalf("读取 FileBrowser 监听地址失败: %v: %s", netstatErr, strings.TrimSpace(string(netstatOutput)))
		}
		listener := ""
		for _, line := range strings.Split(string(netstatOutput), "\n") {
			if strings.Contains(line, ".19445") && strings.Contains(line, "LISTEN") {
				listener = line
				break
			}
		}
		if listener == "" || !strings.Contains(listener, "127.0.0.1.19445") || strings.Contains(listener, "*.19445") {
			t.Fatalf("FileBrowser 未仅监听 IPv4 回环地址: %q", listener)
		}
	}

	query := url.Values{"path": {"/"}, "source": {"文稿"}}
	response, err := client.Get(FileBrowserURL + "/api/resources?" + query.Encode())
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("FileBrowser 无法浏览默认来源: HTTP %d", response.StatusCode)
	}

	query.Set("path", "/sample.txt")
	request, err := http.NewRequestWithContext(t.Context(), http.MethodDelete, FileBrowserURL+"/api/resources?"+query.Encode(), nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("FileBrowser 删除请求未被拒绝: HTTP %d", response.StatusCode)
	}
}

func TestWaitForJellyfinStartupConfigurationWaitsForJSONBeforeConfiguration(t *testing.T) {
	requests := make(chan string, 3)
	startupRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests <- request.Method + " " + request.URL.Path
		if request.URL.Path != "/Startup/Configuration" {
			http.NotFound(writer, request)
			return
		}
		startupRequests++
		if startupRequests == 1 {
			http.Error(writer, "Jellyfin Server still starting. Please wait.", http.StatusServiceUnavailable)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"UICulture":"zh-CN"}`))
	}))
	defer server.Close()

	ready, err := waitForJellyfinStartupConfiguration(t.Context(), server.Client(), server.URL, time.Millisecond, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !ready {
		t.Fatal("首次启动配置返回合法 JSON 后应允许 Startup 写请求")
	}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+"/Startup/Configuration", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()

	want := []string{
		"GET /Startup/Configuration",
		"GET /Startup/Configuration",
		"POST /Startup/Configuration",
	}
	for index, expected := range want {
		select {
		case actual := <-requests:
			if actual != expected {
				t.Fatalf("请求顺序错误: 第 %d 个请求=%q, want=%q", index+1, actual, expected)
			}
		case <-time.After(time.Second):
			t.Fatalf("未收到第 %d 个请求", index+1)
		}
	}
}

func TestWaitForJellyfinStartupConfigurationAcceptsCompletedWizard(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/Startup/Configuration":
			http.Error(writer, "Unauthorized", http.StatusUnauthorized)
		case "/System/Info/Public":
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"StartupWizardCompleted":true}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	ready, err := waitForJellyfinStartupConfiguration(t.Context(), server.Client(), server.URL, time.Millisecond, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if ready {
		t.Fatal("已完成向导的 Jellyfin 不应再次授权 Startup 写请求")
	}
}

func TestWaitForJellyfinStartupConfigurationAllowsListenerHandoffAfter503(t *testing.T) {
	requests := 0
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		switch requests {
		case 1:
			return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: io.NopCloser(strings.NewReader("still starting")), Header: make(http.Header)}, nil
		case 2:
			return nil, errors.New("dial tcp 127.0.0.1:19446: connect: connection refused")
		default:
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"UICulture":"zh-CN"}`)), Header: make(http.Header)}, nil
		}
	})}

	ready, err := waitForJellyfinStartupConfiguration(t.Context(), client, "http://127.0.0.1:19446", time.Millisecond, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !ready || requests != 3 {
		t.Fatalf("Jellyfin 监听器切换后未恢复首次配置: ready=%v requests=%d", ready, requests)
	}
}

func TestWaitForJellyfinStartupConfigurationRetriesPublicInfo503AfterUnauthorized(t *testing.T) {
	publicRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/Startup/Configuration":
			writer.WriteHeader(http.StatusUnauthorized)
		case "/System/Info/Public":
			publicRequests++
			if publicRequests == 1 {
				writer.WriteHeader(http.StatusServiceUnavailable)
				_, _ = writer.Write([]byte("Jellyfin 服务器加载中"))
				return
			}
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"StartupWizardCompleted":true}`))
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	ready, err := waitForJellyfinStartupConfiguration(t.Context(), server.Client(), server.URL, time.Millisecond, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if ready || publicRequests != 2 {
		t.Fatalf("Jellyfin 完成向导后的 503 未被正确重试: ready=%v publicRequests=%d", ready, publicRequests)
	}
}

func TestConfigureJellyfinStartupInitializesFirstUserBeforeUpdate(t *testing.T) {
	requests := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests = append(requests, request.Method+" "+request.URL.Path)
		if request.Method == http.MethodGet && request.URL.Path == "/Startup/User" {
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"Name":"default"}`))
			return
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	if err := configureJellyfinStartup(t.Context(), server.Client(), server.URL, "password"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"POST /Startup/Configuration",
		"GET /Startup/User",
		"POST /Startup/User",
		"POST /Startup/RemoteAccess",
		"POST /Startup/Complete",
	}
	if len(requests) != len(want) {
		t.Fatalf("Jellyfin 向导请求数量错误: got=%v want=%v", requests, want)
	}
	for index, expected := range want {
		if requests[index] != expected {
			t.Fatalf("Jellyfin 向导请求顺序错误: got=%v want=%v", requests, want)
		}
	}
}

func TestJellyfinTimeoutsKeepHealthRequestsShortAndLibraryOperationsLong(t *testing.T) {
	if jellyfinRequestTimeout >= jellyfinLibraryTimeout {
		t.Fatalf("Jellyfin 普通请求超时必须短于媒体库操作: request=%s library=%s", jellyfinRequestTimeout, jellyfinLibraryTimeout)
	}
	if jellyfinRequestTimeout != 20*time.Second {
		t.Fatalf("Jellyfin 普通请求超时错误: %s", jellyfinRequestTimeout)
	}
	if jellyfinLibraryTimeout != 2*time.Minute {
		t.Fatalf("Jellyfin 媒体库操作超时错误: %s", jellyfinLibraryTimeout)
	}
}

func TestConfigureJellyfinReusesExistingAPIKeyWithoutPasswordLogin(t *testing.T) {
	requests := []string{}
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests = append(requests, request.Method+" "+request.URL.Path)
		if request.Header.Get("X-Emby-Token") != "existing-api-key" {
			t.Fatalf("Jellyfin 请求未使用既有 API Key: %s", request.URL.Path)
		}
		switch request.Method + " " + request.URL.Path {
		case "GET /Library/VirtualFolders":
			return jsonResponse(http.StatusOK, `[{"Name":"HomeStack","Locations":["/media"]}]`), nil
		default:
			return jsonResponse(http.StatusNotFound, `{"error":"unexpected"}`), nil
		}
	})}
	directories := []protocol.SharedDirectory{{ID: "media", Name: "影视", Path: "/media"}}
	apiKey, err := configureJellyfin(t.Context(), client, "stale-password", "existing-api-key", directories, false)
	if err != nil {
		t.Fatal(err)
	}
	if apiKey != "existing-api-key" || len(requests) != 1 || requests[0] != "GET /Library/VirtualFolders" {
		t.Fatalf("既有 API Key 迁移执行了多余认证: key=%v requests=%v", apiKey != "", requests)
	}
}

func TestConfigureJellyfinDoesNotFallbackWhenExistingAPIKeyIsRejected(t *testing.T) {
	requests := []string{}
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests = append(requests, request.Method+" "+request.URL.Path)
		return jsonResponse(http.StatusUnauthorized, `{"error":"invalid api key"}`), nil
	})}
	_, err := configureJellyfin(t.Context(), client, "password-must-not-be-used", "invalid-api-key", []protocol.SharedDirectory{{ID: "media", Name: "影视", Path: "/media"}}, false)
	if err == nil || !strings.Contains(err.Error(), "/Library/VirtualFolders 返回 HTTP 401") {
		t.Fatalf("无效 API Key 未返回真实错误: %v", err)
	}
	if len(requests) != 1 || requests[0] != "GET /Library/VirtualFolders" {
		t.Fatalf("无效 API Key 后不应降级到密码认证: %v", requests)
	}
}

func TestConfigureJellyfinDoesNotUsePasswordWhenConfiguredAPIKeyIsMissing(t *testing.T) {
	requestCount := 0
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requestCount++
		return jsonResponse(http.StatusInternalServerError, `{}`), nil
	})}
	_, err := configureJellyfin(t.Context(), client, "password-must-not-be-used", "", []protocol.SharedDirectory{{ID: "media", Name: "影视", Path: "/media"}}, false)
	if err == nil || !strings.Contains(err.Error(), "缺少 HomeStack API Key") {
		t.Fatalf("缺失 API Key 未返回真实错误: %v", err)
	}
	if requestCount != 0 {
		t.Fatalf("缺失 API Key 后不应执行密码认证: requests=%d", requestCount)
	}
}

func TestRecoverJellyfinAPIKeyFromConfiguredDatabase(t *testing.T) {
	stateDir := createJellyfinAPIKeyDatabase(t, []string{"recovered-api-key"})
	apiKey, err := recoverJellyfinAPIKey(t.Context(), stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if apiKey != "recovered-api-key" {
		t.Fatal("未恢复预期的 Jellyfin API Key")
	}
}

func TestRecoverJellyfinAPIKeyRejectsMissingOrAmbiguousCredentials(t *testing.T) {
	for _, test := range []struct {
		name    string
		keys    []string
		message string
	}{
		{name: "missing", keys: nil, message: "未找到 HomeStack API Key"},
		{name: "empty", keys: []string{""}, message: "API Key 为空"},
		{name: "ambiguous", keys: []string{"first-secret", "second-secret"}, message: "存在多个 HomeStack API Key"},
		{name: "ambiguous with empty first key", keys: []string{"", "second-secret"}, message: "存在多个 HomeStack API Key"},
	} {
		t.Run(test.name, func(t *testing.T) {
			stateDir := createJellyfinAPIKeyDatabase(t, test.keys)
			_, err := recoverJellyfinAPIKey(t.Context(), stateDir)
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("凭据异常未返回预期错误: %v", err)
			}
			for _, key := range test.keys {
				if key != "" && strings.Contains(err.Error(), key) {
					t.Fatal("凭据恢复错误泄露了 API Key")
				}
			}
		})
	}
}

func TestSQLiteReadOnlyDSNRejectsWrites(t *testing.T) {
	stateDir := createJellyfinAPIKeyDatabase(t, []string{"existing-key"})
	dsn, err := sqliteReadOnlyDSN(filepath.Join(stateDir, "jellyfin", "data", "data", "jellyfin.db"))
	if err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`DELETE FROM "ApiKeys"`); err == nil {
		t.Fatal("Jellyfin 凭据迁移数据库连接允许写入")
	}
}

func createJellyfinAPIKeyDatabase(t *testing.T, keys []string) string {
	t.Helper()
	stateDir := t.TempDir()
	databaseDir := filepath.Join(stateDir, "jellyfin", "data", "data")
	if err := os.MkdirAll(databaseDir, 0o700); err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", filepath.Join(databaseDir, "jellyfin.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if _, err := database.Exec(`CREATE TABLE "ApiKeys" ("Name" TEXT NOT NULL, "AccessToken" TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for _, key := range keys {
		if _, err := database.Exec(`INSERT INTO "ApiKeys" ("Name", "AccessToken") VALUES (?, ?)`, "HomeStack", key); err != nil {
			t.Fatal(err)
		}
	}
	return stateDir
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
