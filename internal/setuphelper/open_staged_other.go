//go:build !linux

package setuphelper

import (
	"errors"
	"os"
)

func openStagedArchive(path string) (*os.File, error) {
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() {
		return nil, errors.New("Control 更新暂存资产必须是常规文件")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) {
		_ = file.Close()
		return nil, errors.New("Control 更新暂存资产在打开期间发生变化")
	}
	return file, nil
}
