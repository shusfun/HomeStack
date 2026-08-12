//go:build !windows

package desktop

import "errors"

func AcquireNodeInstance() (func(), error) {
	return func() {}, nil
}

func configureWindowsStartup(string) error {
	return errors.New("Windows Node 自启动只能在 Windows 上配置")
}

func restartWindowsNode(string) error {
	return errors.New("Windows Node 只能在 Windows 上重启")
}
