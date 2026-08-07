package publicurl

import (
	"errors"
	"net"
	"net/url"
	"strings"
)

type Address struct {
	Host string
	URL  string
}

func Normalize(raw string) (Address, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return Address{}, errors.New("地址不能为空")
	}
	if !strings.Contains(value, "://") {
		value = "https://" + value
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.Port() != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.RawPath != "" || (parsed.Path != "" && parsed.Path != "/") {
		return Address{}, errors.New("必须是无端口、无路径的 HTTPS 完整域名")
	}
	host := strings.ToLower(parsed.Hostname())
	if err := validateHostname(host); err != nil {
		return Address{}, err
	}
	return Address{Host: host, URL: "https://" + host}, nil
}

func validateHostname(value string) error {
	if len(value) > 253 || net.ParseIP(value) != nil {
		return errors.New("必须是完整域名，不能使用 IP 地址")
	}
	labels := strings.Split(value, ".")
	if len(labels) < 2 {
		return errors.New("必须是完整域名")
	}
	for _, label := range labels {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return errors.New("域名标签格式无效")
		}
		for _, character := range label {
			if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
				return errors.New("域名仅允许小写字母、数字、连字符和点")
			}
		}
	}
	return nil
}
