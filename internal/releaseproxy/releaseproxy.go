package releaseproxy

import (
	"errors"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

const (
	Host              = "gh-proxy.com"
	Prefix            = "https://" + Host + "/"
	officialHost      = "github.com"
	officialPathStart = "/shusfun/HomeStack/releases/"
)

type transport struct {
	base http.RoundTripper
}

func NewClient(timeout time.Duration) *http.Client {
	return &http.Client{Transport: transport{base: http.DefaultTransport}, Timeout: timeout}
}

func ProxyURL(official string) (string, error) {
	parsed, err := url.Parse(official)
	if err != nil || !isOfficialURL(parsed) {
		return "", errors.New("HomeStack Release 官方地址无效")
	}
	return Prefix + parsed.String(), nil
}

func IsOfficialURL(raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil && isOfficialURL(parsed)
}

func IsProxyURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() != Host || parsed.Port() != "" || parsed.User != nil || parsed.Fragment != "" || parsed.RawPath != "" {
		return false
	}
	official, err := url.Parse(strings.TrimPrefix(parsed.Path, "/"))
	if err != nil {
		return false
	}
	official.RawQuery = parsed.RawQuery
	return isOfficialURL(official)
}

func (t transport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request == nil || request.URL == nil {
		return nil, errors.New("HomeStack Release 请求为空")
	}
	target := request.URL
	switch strings.ToLower(target.Hostname()) {
	case officialHost:
		proxied, err := ProxyURL(target.String())
		if err != nil {
			return nil, err
		}
		target, err = url.Parse(proxied)
		if err != nil {
			return nil, err
		}
	case Host:
		if !IsProxyURL(target.String()) {
			return nil, errors.New("HomeStack Release 固定代理地址无效")
		}
	default:
		return nil, errors.New("HomeStack Release 请求主机不受支持")
	}
	clone := request.Clone(request.Context())
	clone.URL = target
	clone.Host = ""
	return t.base.RoundTrip(clone)
}

func isOfficialURL(parsed *url.URL) bool {
	if parsed == nil || parsed.Scheme != "https" || parsed.Hostname() != officialHost || parsed.Port() != "" || parsed.User != nil || parsed.Fragment != "" || parsed.RawPath != "" {
		return false
	}
	if !strings.HasPrefix(parsed.Path, officialPathStart) || path.Clean(parsed.Path) != parsed.Path || strings.ContainsAny(parsed.Path, "\\\r\n\x00") {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(parsed.Path, officialPathStart), "/")
	if len(parts) != 3 || parts[2] == "" {
		return false
	}
	return parts[0] == "latest" && parts[1] == "download" || parts[0] == "download" && parts[1] != ""
}
