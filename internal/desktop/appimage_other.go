//go:build !linux

package desktop

import (
	"errors"

	"github.com/wailsapp/wails/v3/pkg/application"
)

func restartAppImage(*application.App, string) error {
	return errors.New("AppImage 更新只支持 Linux")
}

func RunAppImageUpdateHelper([]string) error {
	return errors.New("AppImage 更新 helper 只支持 Linux")
}
