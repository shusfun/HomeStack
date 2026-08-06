package main

import "testing"

func TestValidateAgentAddressAcceptsOnlyTailnetPort(t *testing.T) {
	for _, address := range []string{"100.64.0.10:9443", "[fd7a:115c:a1e0::10]:9443"} {
		if err := validateAgentAddress(address); err != nil {
			t.Errorf("合法 Tailscale 地址 %q 被拒绝: %v", address, err)
		}
	}

	for _, address := range []string{
		"0.0.0.0:9443", "192.168.1.10:9443", "100.64.0.10:443", "nas.example.com:9443",
	} {
		if err := validateAgentAddress(address); err == nil {
			t.Errorf("非 Tailscale Agent 监听地址 %q 不应被接受", address)
		}
	}
}
