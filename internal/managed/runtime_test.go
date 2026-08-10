package managed

import (
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
