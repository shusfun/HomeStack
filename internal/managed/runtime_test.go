package managed

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteJellyfinNetworkConfigRestrictsToLoopback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "network.xml")
	if err := os.WriteFile(path, []byte("<NetworkConfiguration><LocalNetworkAddresses /></NetworkConfiguration>"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeJellyfinNetworkConfig(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var configuration jellyfinNetworkConfiguration
	if err := xml.Unmarshal(data, &configuration); err != nil {
		t.Fatal(err)
	}
	if configuration.InternalHTTPPort != 19446 || configuration.PublicHTTPPort != 19446 {
		t.Fatalf("Jellyfin 端口未固定: internal=%d public=%d", configuration.InternalHTTPPort, configuration.PublicHTTPPort)
	}
	if !configuration.EnableIPv4 || configuration.EnableIPv6 || configuration.EnableRemoteAccess {
		t.Fatalf("Jellyfin 网络开关错误: %+v", configuration)
	}
	if len(configuration.LocalNetworkAddresses.Values) != 1 || configuration.LocalNetworkAddresses.Values[0] != "127.0.0.1" {
		t.Fatalf("Jellyfin 未限制回环监听: %v", configuration.LocalNetworkAddresses.Values)
	}
}
