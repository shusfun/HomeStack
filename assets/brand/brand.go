package brand

import (
	"bytes"
	_ "embed"
)

//go:embed homestack.png
var appIconPNG []byte

// AppIconPNG 返回 Wails 使用的应用图标副本。
func AppIconPNG() []byte {
	return bytes.Clone(appIconPNG)
}
