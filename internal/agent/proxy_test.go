package agent

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFileBrowserProxyRejectsWritesAndTraversal(t *testing.T) {
	proxy, err := NewFileBrowserProxy("http://127.0.0.1:8080", "secret")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		method string
		target string
	}{
		{method: http.MethodDelete, target: "/api/resources?path=/media/movie.mp4"},
		{method: http.MethodGet, target: "/api/resources?path=/%252e%252e/etc/passwd"},
		{method: http.MethodGet, target: "/api/users"},
	} {
		request := httptest.NewRequest(test.method, test.target, nil)
		response := httptest.NewRecorder()
		proxy.ServeHTTP(response, request)
		if response.Code < 400 {
			t.Fatalf("请求 %s %s 不应通过，状态码为 %d", test.method, test.target, response.Code)
		}
	}
}

func TestModuleTargetMustBeLoopback(t *testing.T) {
	if _, err := NewFileBrowserProxy("http://192.0.2.10:8080", "secret"); err == nil {
		t.Fatal("非回环 FileBrowser 地址不应被接受")
	}
}
