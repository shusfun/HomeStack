package setup

import (
	"errors"
	"fmt"
	"net"
	"strings"
)

func ValidateConfiguration(config Configuration) error {
	hosts := map[string]string{
		"Control 域名":   config.ControlHost,
		"Pocket ID 域名": config.PocketHost,
		"Headscale 域名": config.MeshHost,
		"Tailnet 基础域名": config.TailHost,
	}
	seen := map[string]string{}
	for label, raw := range hosts {
		host := strings.ToLower(strings.TrimSpace(raw))
		if err := validateHostname(host); err != nil {
			return fmt.Errorf("%s无效: %w", label, err)
		}
		if previous := seen[host]; previous != "" {
			return fmt.Errorf("%s不能与%s相同", label, previous)
		}
		seen[host] = label
	}
	ip := net.ParseIP(strings.TrimSpace(config.PublicIPv4))
	if ip == nil || ip.To4() == nil || ip.IsLoopback() || ip.IsUnspecified() || ip.IsPrivate() {
		return errors.New("VPS 公网 IPv4 必须是明确的公网 IPv4 地址")
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
