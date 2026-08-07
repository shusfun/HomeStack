package publicurl

import "testing"

func TestNormalize(t *testing.T) {
	for _, input := range []string{"hs.waasabi.cloud", "https://hs.waasabi.cloud", "https://hs.waasabi.cloud/", "  HTTPS://HS.WAASABI.CLOUD/  "} {
		actual, err := Normalize(input)
		if err != nil {
			t.Fatalf("规范化 %q 失败: %v", input, err)
		}
		if actual.Host != "hs.waasabi.cloud" || actual.URL != "https://hs.waasabi.cloud" {
			t.Fatalf("规范化结果错误: %+v", actual)
		}
	}
}

func TestNormalizeRejectsUnsafeAddresses(t *testing.T) {
	for _, input := range []string{"", "http://hs.waasabi.cloud", "https://hs.waasabi.cloud:443", "https://user@hs.waasabi.cloud", "https://hs.waasabi.cloud/path", "https://hs.waasabi.cloud?x=1", "https://hs.waasabi.cloud/#x", "127.0.0.1", "localhost"} {
		if _, err := Normalize(input); err == nil {
			t.Fatalf("应拒绝地址 %q", input)
		}
	}
}
