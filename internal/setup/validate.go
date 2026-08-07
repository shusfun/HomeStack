package setup

import (
	"errors"
	"net"
	"strings"
)

func ValidateConfiguration(config Configuration) error {
	if err := validateHostname(strings.ToLower(strings.TrimSpace(config.PublicHost))); err != nil {
		return errors.New("VPS 域名无效: " + err.Error())
	}
	if config.Provider != "google" && config.Provider != "github" {
		return errors.New("登录方式只能选择 Google 或 GitHub")
	}
	if strings.TrimSpace(config.ClientID) == "" || strings.TrimSpace(config.ClientSecret) == "" {
		return errors.New("OAuth Client ID 和 Client Secret 必须完整填写")
	}
	if strings.ContainsAny(config.ClientID, "\r\n\x00") || strings.ContainsAny(config.ClientSecret, "\r\n\x00") {
		return errors.New("OAuth 凭据包含非法控制字符")
	}
	return nil
}

func validateHostname(value string) error {
	if value == "" || len(value) > 253 || strings.ContainsAny(value, "/:@?#[]") || net.ParseIP(value) != nil {
		return errors.New("必须是无端口、无路径的完整域名")
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
				return errors.New("仅允许小写字母、数字、连字符和点")
			}
		}
	}
	return nil
}
