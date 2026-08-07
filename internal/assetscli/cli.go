package assetscli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const moduleName = "github.com/wangshangbin/homestack"

type runner struct {
	workingDirectory func() (string, error)
	generateIcons    iconGenerator
}

// Run 执行仓库资产命令并返回进程退出码。
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	current := runner{
		workingDirectory: os.Getwd,
		generateIcons:    wailsIconGenerator{},
	}
	return current.run(ctx, args, stdout, stderr)
}

func (current runner) run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && isHelp(args[0]) {
		writeUsage(stdout)
		return 0
	}
	if len(args) == 2 && args[0] == "icons" && isHelp(args[1]) {
		writeUsage(stdout)
		return 0
	}
	if len(args) != 2 || args[0] != "icons" || (args[1] != "sync" && args[1] != "verify") {
		writeUsage(stderr)
		return 2
	}

	workingDirectory, err := current.workingDirectory()
	if err != nil {
		fmt.Fprintf(stderr, "读取当前目录失败: %v\n", err)
		return 1
	}
	repositoryRoot, err := findRepositoryRoot(workingDirectory)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	brandDirectory := filepath.Join(repositoryRoot, "assets", "brand")
	if args[1] == "sync" {
		err = syncIcons(ctx, repositoryRoot, brandDirectory, current.generateIcons)
	} else {
		err = verifyIcons(ctx, repositoryRoot, brandDirectory, current.generateIcons)
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	fmt.Fprintf(stdout, "品牌图标 %s 完成: %s\n", map[string]string{"sync": "同步", "verify": "校验"}[args[1]], brandDirectory)
	return 0
}

func writeUsage(writer io.Writer) {
	fmt.Fprintln(writer, "用法: homestack-assets icons <sync|verify>")
	fmt.Fprintln(writer, "  sync    从 assets/brand/homestack.png 生成并同步 ICO、ICNS")
	fmt.Fprintln(writer, "  verify  重生成并校验品牌资产和仓库单一来源")
}

func isHelp(argument string) bool {
	return argument == "help" || argument == "-h" || argument == "--help"
}

func findRepositoryRoot(start string) (string, error) {
	current, err := filepath.Abs(start)
	if err != nil {
		return "", fmt.Errorf("解析当前目录失败: %w", err)
	}
	for {
		moduleFile := filepath.Join(current, "go.mod")
		data, readErr := os.ReadFile(moduleFile)
		if readErr == nil {
			if hasExpectedModule(data) {
				return current, nil
			}
			return "", fmt.Errorf("仓库模块不匹配: %s 必须声明 module %s", moduleFile, moduleName)
		}
		if !errors.Is(readErr, os.ErrNotExist) {
			return "", fmt.Errorf("读取 %s 失败: %w", moduleFile, readErr)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("从 %s 向上未找到 HomeStack go.mod", start)
		}
		current = parent
	}
}

func hasExpectedModule(data []byte) bool {
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "module" {
			return fields[1] == moduleName
		}
	}
	return false
}
