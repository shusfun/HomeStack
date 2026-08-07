package brand

import (
	"bytes"
	"image/png"
	"testing"
)

func TestAppIconPNG(t *testing.T) {
	first := AppIconPNG()
	configuration, err := png.DecodeConfig(bytes.NewReader(first))
	if err != nil {
		t.Fatalf("解析嵌入 PNG 失败: %v", err)
	}
	if configuration.Width != 1024 || configuration.Height != 1024 {
		t.Fatalf("嵌入 PNG 尺寸为 %d×%d，预期 1024×1024", configuration.Width, configuration.Height)
	}

	first[0] = 0
	second := AppIconPNG()
	if len(second) == 0 || second[0] == 0 {
		t.Fatal("AppIconPNG 必须返回独立副本")
	}
}
