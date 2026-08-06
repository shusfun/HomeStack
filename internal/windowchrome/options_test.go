package windowchrome

import (
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"
)

func TestApplyUsesUnifiedMacAndFramelessOtherPlatforms(t *testing.T) {
	mac := application.WebviewWindowOptions{}
	Apply(&mac, "darwin")
	if mac.Frameless || mac.Mac.TitleBar != application.MacTitleBarHiddenInsetUnified {
		t.Fatalf("macOS 标题栏配置错误: %+v", mac)
	}
	windows := application.WebviewWindowOptions{}
	Apply(&windows, "windows")
	if !windows.Frameless {
		t.Fatal("Windows 必须使用 frameless 自绘标题栏")
	}
}
