//go:build !linux

package helper

import (
	"context"
	"errors"
)

func Run(context.Context, string, uint32) error {
	return errors.New("homestack-helper 只支持 Linux")
}
