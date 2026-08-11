package desktop

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wangshangbin/homestack/internal/tailscale"
	"github.com/zalando/go-keyring"
)

func TestCallbackHandlerRejectsWrongStateAndAcceptsCode(t *testing.T) {
	codes := make(chan string, 1)
	failures := make(chan error, 1)
	handler := callbackHandler("expected-state", codes, failures)

	wrong := httptest.NewRequest(http.MethodGet, "/callback?state=wrong&code=unused", nil)
	wrongResponse := httptest.NewRecorder()
	handler.ServeHTTP(wrongResponse, wrong)
	if wrongResponse.Code != http.StatusBadRequest {
		t.Fatalf("错误 state 必须返回 400，实际 %d", wrongResponse.Code)
	}
	select {
	case code := <-codes:
		t.Fatalf("错误 state 不应交付授权码: %s", code)
	default:
	}

	valid := httptest.NewRequest(http.MethodGet, "/callback?state=expected-state&code=single-use-code", nil)
	validResponse := httptest.NewRecorder()
	handler.ServeHTTP(validResponse, valid)
	if validResponse.Code != http.StatusOK || <-codes != "single-use-code" {
		t.Fatalf("合法回环授权码未被接收: %d", validResponse.Code)
	}
}

func TestValidateControlURLRequiresOriginOnlyHTTPS(t *testing.T) {
	valid, err := validateControlURL("https://control.example.com/")
	if err != nil || valid != "https://control.example.com" {
		t.Fatalf("合法 Control URL 被拒绝: %q %v", valid, err)
	}
	for _, value := range []string{
		"http://control.example.com", "https://user@control.example.com", "https://control.example.com/path",
		"https://control.example.com?query=1", "https://control.example.com/#fragment",
	} {
		if _, err := validateControlURL(value); err == nil {
			t.Fatalf("非法 Control URL 不应通过: %s", value)
		}
	}
}

func TestWriteRequestIncludesControlOrigin(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			t.Fatalf("请求方法错误: %s", request.Method)
		}
		if request.Header.Get("Origin") != "http://"+request.Host {
			t.Fatalf("写请求 Origin 错误: %q", request.Header.Get("Origin"))
		}
		writer.WriteHeader(http.StatusCreated)
		_, _ = writer.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	client := &APIClient{HTTPClient: server.Client()}
	var response struct {
		OK bool `json:"ok"`
	}
	if err := client.requestJSON(context.Background(), http.MethodPost, server.URL+"/api/devices/device-1/tickets", "token", map[string]any{}, &response, http.StatusCreated); err != nil {
		t.Fatal(err)
	}
	if !response.OK {
		t.Fatal("写请求响应未正确解码")
	}
}

func TestCoreRegistrationExcludesManagedDirectoriesAndModules(t *testing.T) {
	keyring.MockInit()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"signing_key_id":"control","signing_public_key":"unused"}`))
	}))
	defer server.Close()

	client := &APIClient{HTTPClient: server.Client()}
	_, request, _, err := client.registrationRequest(context.Background(), server.URL, tailscale.Status{
		Online: true, TailscaleIP: "100.64.0.1", MagicDNS: "device.tailnet.ts.net",
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(request.SharedDirectories) != 0 || len(request.Modules) != 0 {
		t.Fatalf("核心登记不应包含托管配置: directories=%+v modules=%+v", request.SharedDirectories, request.Modules)
	}
}
