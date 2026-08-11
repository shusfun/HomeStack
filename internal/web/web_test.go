package web

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerServesEntryWithoutCaching(t *testing.T) {
	for _, target := range []string{"/", "/index.html", "/settings/updates"} {
		request := httptest.NewRequest(http.MethodGet, target, nil)
		response := httptest.NewRecorder()
		Handler().ServeHTTP(response, request)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `<div id="root">`) {
			t.Fatalf("%s 未返回 SPA 入口: HTTP %d", target, response.Code)
		}
		if response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s 缓存策略错误: %q", target, response.Header().Get("Cache-Control"))
		}
	}
}

func TestHandlerCachesHashedAssetsImmutably(t *testing.T) {
	entries, err := fs.ReadDir(Assets(), "assets")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() == "app.js" || entry.Name() == "app.css" {
			continue
		}
		request := httptest.NewRequest(http.MethodGet, "/assets/"+entry.Name(), nil)
		response := httptest.NewRecorder()
		Handler().ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("哈希资源未返回 200: %s HTTP %d", entry.Name(), response.Code)
		}
		if response.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
			t.Fatalf("哈希资源缓存策略错误: %s %q", entry.Name(), response.Header().Get("Cache-Control"))
		}
		return
	}
	t.Fatal("构建产物中没有可验证的哈希资源")
}

func TestHandlerServesStableEntrypointsWithoutCaching(t *testing.T) {
	for _, target := range []string{"/assets/app.js", "/assets/app.css"} {
		request := httptest.NewRequest(http.MethodGet, target, nil)
		response := httptest.NewRecorder()
		Handler().ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("%s 未返回构建入口: HTTP %d", target, response.Code)
		}
		if response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s 缓存策略错误: %q", target, response.Header().Get("Cache-Control"))
		}
	}
}

func TestHandlerReturnsRealNotFoundForMissingAssetsAndFiles(t *testing.T) {
	for _, target := range []string{"/assets/missing.js", "/missing.css"} {
		request := httptest.NewRequest(http.MethodGet, target, nil)
		response := httptest.NewRecorder()
		Handler().ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Fatalf("%s 应返回 404，实际 HTTP %d", target, response.Code)
		}
		if strings.Contains(response.Body.String(), `<div id="root">`) {
			t.Fatalf("%s 不应回退到 SPA 入口", target)
		}
	}
}

func TestHandlerRejectsMutation(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	response := httptest.NewRecorder()
	Handler().ServeHTTP(response, request)
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST 应返回 405，实际 HTTP %d", response.Code)
	}
}
