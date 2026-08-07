package node

import "testing"

func TestValidateAddressAcceptsOnlyLoopbackNodePort(t *testing.T) {
	for _, address := range []string{"127.0.0.1:19444", "[::1]:19444"} {
		if err := ValidateAddress(address); err != nil {
			t.Errorf("合法 Node 地址 %q 被拒绝: %v", address, err)
		}
	}
	for _, address := range []string{"0.0.0.0:19444", "100.64.0.10:19444", "127.0.0.1:443", "localhost:19444"} {
		if err := ValidateAddress(address); err == nil {
			t.Errorf("非回环 Node 地址 %q 不应被接受", address)
		}
	}
}
