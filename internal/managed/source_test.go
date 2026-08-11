package managed

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSelectSourceChoosesFastestValidRange(t *testing.T) {
	assetSize := int64(128 << 10)
	server := func(delay time.Duration, valid bool) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			time.Sleep(delay)
			if !valid {
				writer.WriteHeader(http.StatusOK)
				return
			}
			writer.Header().Set("Content-Range", fmt.Sprintf("bytes 0-%d/%d", sourceProbeBytes-1, assetSize))
			writer.WriteHeader(http.StatusPartialContent)
			_, _ = writer.Write(make([]byte, sourceProbeBytes))
		}))
	}
	slow, fast, invalid := server(30*time.Millisecond, true), server(time.Millisecond, true), server(0, false)
	defer slow.Close()
	defer fast.Close()
	defer invalid.Close()
	artifact := Artifact{Component: "filebrowser", Size: assetSize, URLs: []string{slow.URL, invalid.URL, fast.URL}}
	selected, _, err := selectSource(context.Background(), fast.Client(), artifact, nil)
	if err != nil {
		t.Fatal(err)
	}
	if selected != fast.URL {
		t.Fatalf("未选择最快有效源: %s", selected)
	}
}

func TestSelectSourceRejectsAllInvalidCandidates(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { writer.WriteHeader(http.StatusOK) }))
	defer server.Close()
	_, _, err := selectSource(context.Background(), server.Client(), Artifact{Component: "filebrowser", Size: 1024, URLs: []string{server.URL, server.URL + "/other"}}, nil)
	if err == nil {
		t.Fatal("全部不支持 Range 的候选源未返回错误")
	}
}

func TestSelectSourceRejectsInvalidContentRange(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Range", "bytes 0-1023/2048")
		writer.WriteHeader(http.StatusPartialContent)
		_, _ = writer.Write(make([]byte, 1024))
	}))
	defer server.Close()
	_, _, err := selectSource(context.Background(), server.Client(), Artifact{Component: "jellyfin", Size: 1024, URLs: []string{server.URL}}, nil)
	if err == nil {
		t.Fatal("总大小不匹配的 Content-Range 未被拒绝")
	}
}

func TestSelectSourceStopsWhenCancelled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		<-request.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := selectSource(ctx, server.Client(), Artifact{Component: "filebrowser", Size: 1024, URLs: []string{server.URL}}, nil)
	if err == nil {
		t.Fatal("已取消的测速未返回错误")
	}
}

func TestSelectSourceRejectsInvalidTLS(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusPartialContent)
	}))
	defer server.Close()
	_, _, err := selectSource(context.Background(), http.DefaultClient, Artifact{Component: "filebrowser", Size: 1024, URLs: []string{server.URL}}, nil)
	if err == nil {
		t.Fatal("证书不受信任的 HTTPS 候选源未被拒绝")
	}
}
