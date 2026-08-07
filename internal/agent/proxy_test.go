package agent

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestJellyfinProxyPreservesRangeHLSAndPlaybackProgress(t *testing.T) {
	requests := make(chan *http.Request, 3)
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests <- request.Clone(request.Context())
		if request.Method == http.MethodGet {
			writer.Header().Set("Content-Range", "bytes 0-3/8")
			writer.WriteHeader(http.StatusPartialContent)
			_, _ = io.WriteString(writer, "data")
			return
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	proxy, err := NewJellyfinProxy(upstream.URL, "media-secret")
	if err != nil {
		t.Fatal(err)
	}

	rangeRequest := httptest.NewRequest(http.MethodGet, "/Videos/item/stream.mp4", nil)
	rangeRequest.Header.Set("Range", "bytes=0-3")
	rangeResponse := httptest.NewRecorder()
	proxy.ServeHTTP(rangeResponse, rangeRequest)
	if rangeResponse.Code != http.StatusPartialContent || rangeResponse.Header().Get("Content-Range") != "bytes 0-3/8" {
		t.Fatalf("媒体 Range 响应未透传: %d %s", rangeResponse.Code, rangeResponse.Header().Get("Content-Range"))
	}
	proxiedRange := <-requests
	if proxiedRange.Header.Get("Range") != "bytes=0-3" || proxiedRange.Header.Get("X-Emby-Token") != "media-secret" {
		t.Fatal("媒体 Range 或 Jellyfin 凭据未正确转发")
	}

	hlsRequest := httptest.NewRequest(http.MethodGet, "/Videos/item/master.m3u8", nil)
	hlsResponse := httptest.NewRecorder()
	proxy.ServeHTTP(hlsResponse, hlsRequest)
	if hlsResponse.Code != http.StatusPartialContent {
		t.Fatalf("HLS 清单请求应通过媒体白名单，实际 %d", hlsResponse.Code)
	}
	<-requests

	progressRequest := httptest.NewRequest(http.MethodPost, "/Sessions/Playing/Progress", strings.NewReader(`{"ItemId":"item"}`))
	progressResponse := httptest.NewRecorder()
	proxy.ServeHTTP(progressResponse, progressRequest)
	if progressResponse.Code != http.StatusNoContent {
		t.Fatalf("Jellyfin 播放进度应通过白名单，实际 %d", progressResponse.Code)
	}
	<-requests

	denied := httptest.NewRequest(http.MethodPost, "/Arbitrary/Playing/Progress/Action", strings.NewReader(`{}`))
	deniedResponse := httptest.NewRecorder()
	proxy.ServeHTTP(deniedResponse, denied)
	if deniedResponse.Code != http.StatusForbidden {
		t.Fatalf("非固定播放事件路径必须被拒绝，实际 %d", deniedResponse.Code)
	}
}

func TestModuleTargetMustBeLoopback(t *testing.T) {
	if _, err := NewJellyfinProxy("http://192.0.2.10:8096", "secret"); err == nil {
		t.Fatal("非回环 Jellyfin 地址不应被接受")
	}
}
