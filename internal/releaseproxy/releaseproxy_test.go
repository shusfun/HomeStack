package releaseproxy

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestProxyURLAcceptsOnlyHomeStackReleaseAssets(t *testing.T) {
	official := "https://github.com/shusfun/HomeStack/releases/download/v0.2.19/HomeStack_0.2.19_windows_amd64_update.zip"
	proxied, err := ProxyURL(official)
	if err != nil {
		t.Fatal(err)
	}
	if proxied != Prefix+official || !IsOfficialURL(official) || !IsProxyURL(proxied) {
		t.Fatalf("固定代理地址不符合契约: %s", proxied)
	}
	for _, invalid := range []string{
		"http://github.com/shusfun/HomeStack/releases/download/v0.2.19/file.zip",
		"https://github.com/other/HomeStack/releases/download/v0.2.19/file.zip",
		"https://github.com/shusfun/HomeStack/releases/download/v0.2.19/../file.zip",
		"https://ghproxy.net/https://github.com/shusfun/HomeStack/releases/download/v0.2.19/file.zip",
		"https://gh-proxy.com/https://github.com/other/HomeStack/releases/download/v0.2.19/file.zip",
	} {
		if IsOfficialURL(invalid) || IsProxyURL(invalid) {
			t.Fatalf("非法 Release 地址被接受: %s", invalid)
		}
	}
}

func TestTransportRewritesOfficialReleaseRequest(t *testing.T) {
	var received string
	client := &http.Client{Transport: transport{base: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		received = request.URL.String()
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("ok")), Header: make(http.Header), Request: request}, nil
	})}}
	requestURL := "https://github.com/shusfun/HomeStack/releases/latest/download/latest.json?arch=amd64&platform=windows&version=0.2.19"
	response, err := client.Get(requestURL)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if received != Prefix+requestURL {
		t.Fatalf("Release 请求未改写到固定代理: %s", received)
	}
}

func TestTransportAcceptsProxiedManifestQuery(t *testing.T) {
	proxied := Prefix + "https://github.com/shusfun/HomeStack/releases/latest/download/latest.json?arch=amd64&platform=windows&version=0.2.19"
	var received string
	client := &http.Client{Transport: transport{base: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		received = request.URL.String()
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("ok")), Header: make(http.Header), Request: request}, nil
	})}}
	response, err := client.Get(proxied)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if received != proxied || !IsProxyURL(proxied) {
		t.Fatalf("带查询参数的代理清单地址未原样保留: %s", received)
	}
}

func TestTransportRejectsUnapprovedHost(t *testing.T) {
	client := &http.Client{Transport: transport{base: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("非法主机不应进入底层 Transport")
		return nil, nil
	})}}
	if _, err := client.Get("https://example.com/file.zip"); err == nil {
		t.Fatal("非法 Release 请求主机未被拒绝")
	}
}
