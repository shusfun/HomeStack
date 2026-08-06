//go:build contract

package contracts_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wangshangbin/homestack/internal/components"
)

func TestFixedComponentBinaries(t *testing.T) {
	for _, spec := range components.FixedSpecs {
		spec := spec
		t.Run(spec.ID, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if err := components.RequireVersion(ctx, spec); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestHeadscaleConfiguration(t *testing.T) {
	configPath := strings.TrimSpace(os.Getenv("HOMESTACK_CONTRACT_HEADSCALE_CONFIG"))
	if configPath == "" {
		t.Fatal("必须设置 HOMESTACK_CONTRACT_HEADSCALE_CONFIG 指向已替换占位符的 Headscale v0.29.3 配置")
	}
	absolutePath, err := filepath.Abs(configPath)
	if err != nil {
		t.Fatalf("解析 Headscale 配置路径失败: %v", err)
	}
	info, err := os.Stat(absolutePath)
	if err != nil || info.IsDir() {
		t.Fatalf("Headscale 配置不可读: %s: %v", absolutePath, err)
	}

	runHeadscale(t, absolutePath, "configtest")
	runHeadscale(t, absolutePath, "policy", "check", "--bypass")
}

func runHeadscale(t *testing.T, configPath string, arguments ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	args := append([]string{"--config", configPath}, arguments...)
	output, err := exec.CommandContext(ctx, "headscale", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("Headscale 契约命令失败: headscale %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	if ctx.Err() != nil {
		t.Fatal(fmt.Errorf("Headscale 契约命令超时: %w", ctx.Err()))
	}
}
