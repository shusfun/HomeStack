package assetscli

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunnerRejectsInvalidArguments(t *testing.T) {
	current := runner{workingDirectory: os.Getwd, generateIcons: fixtureGenerator{}}
	for _, arguments := range [][]string{nil, {"icons"}, {"unknown", "sync"}, {"icons", "sync", "extra"}} {
		var stderr bytes.Buffer
		if code := current.run(context.Background(), arguments, &bytes.Buffer{}, &stderr); code != 2 {
			t.Fatalf("参数 %v 的退出码为 %d，预期 2", arguments, code)
		}
		if !strings.Contains(stderr.String(), "用法:") {
			t.Fatalf("参数 %v 未输出用法", arguments)
		}
	}
}

func TestRunnerPrintsHelp(t *testing.T) {
	current := runner{workingDirectory: os.Getwd, generateIcons: fixtureGenerator{}}
	for _, arguments := range [][]string{{"--help"}, {"icons", "--help"}} {
		var stdout, stderr bytes.Buffer
		if code := current.run(context.Background(), arguments, &stdout, &stderr); code != 0 {
			t.Fatalf("参数 %v 的退出码为 %d，预期 0", arguments, code)
		}
		if !strings.Contains(stdout.String(), "用法:") || stderr.Len() != 0 {
			t.Fatalf("参数 %v 未正确输出帮助: stdout=%q stderr=%q", arguments, stdout.String(), stderr.String())
		}
	}
}

func TestFindRepositoryRootRejectsMismatchedModule(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "go.mod"), []byte("module example.com/other\n"))
	_, err := findRepositoryRoot(root)
	if err == nil || !strings.Contains(err.Error(), "仓库模块不匹配") {
		t.Fatalf("未拒绝错误模块: %v", err)
	}
}

func TestSyncAndVerifyIcons(t *testing.T) {
	root, brandDirectory := createBrandFixture(t)
	current := runner{
		workingDirectory: func() (string, error) { return filepath.Join(root, "nested"), nil },
		generateIcons:    fixtureGenerator{},
	}
	if err := os.MkdirAll(filepath.Join(root, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"sync", "verify"} {
		var stdout, stderr bytes.Buffer
		if code := current.run(context.Background(), []string{"icons", action}, &stdout, &stderr); code != 0 {
			t.Fatalf("icons %s 失败: %s", action, stderr.String())
		}
	}
	for _, name := range []string{"homestack.ico", "homestack.icns"} {
		if _, err := os.Stat(filepath.Join(brandDirectory, name)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestVerifyRejectsMismatchedGeneratedIcon(t *testing.T) {
	root, brandDirectory := createBrandFixture(t)
	generator := fixtureGenerator{}
	if err := syncIcons(context.Background(), root, brandDirectory, generator); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(brandDirectory, "homestack.icns"), validICNS([]byte("changed")))
	err := verifyIcons(context.Background(), root, brandDirectory, generator)
	if err == nil || !strings.Contains(err.Error(), "与 PNG 不一致") {
		t.Fatalf("未拒绝失配图标: %v", err)
	}
}

func TestVerifyRejectsDuplicateBrandAsset(t *testing.T) {
	root, brandDirectory := createBrandFixture(t)
	if err := syncIcons(context.Background(), root, brandDirectory, fixtureGenerator{}); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(root, "build", "homestack.png"), []byte("duplicate"))
	err := verifyIcons(context.Background(), root, brandDirectory, fixtureGenerator{})
	if err == nil || !strings.Contains(err.Error(), "重复副本") {
		t.Fatalf("未拒绝重复品牌资源: %v", err)
	}
}

func TestValidatePNGRejectsInvalidDimensions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "small.png")
	writePNG(t, path, 32)
	if err := validatePNG(path); err == nil || !strings.Contains(err.Error(), "1024×1024") {
		t.Fatalf("未拒绝错误 PNG 尺寸: %v", err)
	}
}

func TestWailsGeneratorReportsMissingGoToolchain(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	err := (wailsIconGenerator{}).Generate(context.Background(), t.TempDir(), "source.png", "output.ico", "output.icns")
	if err == nil || !strings.Contains(err.Error(), "未找到 Go 工具链") {
		t.Fatalf("未返回 Go 工具链错误: %v", err)
	}
}

func TestSyncDoesNotWriteIconsWhenGenerationFails(t *testing.T) {
	root, brandDirectory := createBrandFixture(t)
	err := syncIcons(context.Background(), root, brandDirectory, failingGenerator{})
	if err == nil || !strings.Contains(err.Error(), "生成失败") {
		t.Fatalf("未返回生成错误: %v", err)
	}
	for _, name := range []string{"homestack.ico", "homestack.icns"} {
		if _, statErr := os.Stat(filepath.Join(brandDirectory, name)); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("生成失败后不应存在 %s: %v", name, statErr)
		}
	}
}

type fixtureGenerator struct{}

func (fixtureGenerator) Generate(_ context.Context, _, _, outputICO, outputICNS string) error {
	if err := os.WriteFile(outputICO, validICO(), 0o644); err != nil {
		return err
	}
	return os.WriteFile(outputICNS, validICNS([]byte("fixture")), 0o644)
}

type failingGenerator struct{}

func (failingGenerator) Generate(context.Context, string, string, string, string) error {
	return errors.New("生成失败")
}

func createBrandFixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	brandDirectory := filepath.Join(root, "assets", "brand")
	writeTestFile(t, filepath.Join(root, "go.mod"), []byte("module "+moduleName+"\n"))
	writeTestFile(t, filepath.Join(brandDirectory, "homestack.svg"), []byte(`<svg viewBox="0 0 1024 1024"><rect fill="#10B981"/><rect fill="#2563EB"/><rect fill="#F8FAFC"/></svg>`))
	writeBrandPNG(t, filepath.Join(brandDirectory, "homestack.png"))
	return root, brandDirectory
}

func writeBrandPNG(t *testing.T, path string) {
	t.Helper()
	canvas := image.NewNRGBA(image.Rect(0, 0, 1024, 1024))
	canvas.SetNRGBA(512, 100, color.NRGBA{248, 250, 252, 255})
	canvas.SetNRGBA(300, 300, color.NRGBA{16, 185, 129, 255})
	canvas.SetNRGBA(700, 300, color.NRGBA{37, 99, 235, 255})
	writePNGImage(t, path, canvas)
}

func writePNG(t *testing.T, path string, size int) {
	t.Helper()
	writePNGImage(t, path, image.NewNRGBA(image.Rect(0, 0, size, size)))
}

func writePNGImage(t *testing.T, path string, source image.Image) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(file, source); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func validICO() []byte {
	data := make([]byte, 6+len(expectedICOSizes)*16)
	binary.LittleEndian.PutUint16(data[2:4], 1)
	binary.LittleEndian.PutUint16(data[4:6], uint16(len(expectedICOSizes)))
	for index, size := range expectedICOSizes {
		offset := 6 + index*16
		if size != 256 {
			data[offset] = byte(size)
			data[offset+1] = byte(size)
		}
	}
	return data
}

func validICNS(payload []byte) []byte {
	data := append([]byte("icns\x00\x00\x00\x00"), payload...)
	binary.BigEndian.PutUint32(data[4:8], uint32(len(data)))
	return data
}

func writeTestFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}
