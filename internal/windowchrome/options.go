package windowchrome

import "github.com/wailsapp/wails/v3/pkg/application"

// Apply 统一 HomeStack 桌面端的平台标题栏行为。
func Apply(options *application.WebviewWindowOptions, goos string) {
	options.Frameless = goos != "darwin"
	if goos == "darwin" {
		options.Mac.TitleBar = application.MacTitleBarHiddenInsetUnified
	}
}
