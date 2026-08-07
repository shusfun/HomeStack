//go:build !linux

package setuphelper

import (
	"context"
	"errors"
)

func Run(context.Context, string, uint32) error {
	return errors.New("homestack-config-helper 只支持 Linux")
}
