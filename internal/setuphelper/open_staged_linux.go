//go:build linux

package setuphelper

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func openStagedArchive(path string) (*os.File, error) {
	descriptor, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(descriptor), path)
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, errors.New("Control 更新暂存资产必须是常规文件")
	}
	return file, nil
}
