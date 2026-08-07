//go:build !linux

package setuphelper

import (
	"context"
	"errors"
)

func RunMaintenance(context.Context, string, uint32) error {
	return errors.New("维护 Helper 只支持 Linux")
}
