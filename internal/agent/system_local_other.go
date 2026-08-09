//go:build !darwin && !linux && !windows

package agent

import (
	"context"
	"errors"
)

func localMetrics(context.Context) (SystemMetrics, error) {
	return SystemMetrics{}, errors.New("当前平台不支持本地系统指标采集")
}
