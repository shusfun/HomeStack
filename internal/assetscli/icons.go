package assetscli

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

var expectedICOSizes = []int{16, 32, 48, 64, 128, 256}

type iconGenerator interface {
	Generate(context.Context, string, string, string, string) error
}

type wailsIconGenerator struct{}

func (wailsIconGenerator) Generate(ctx context.Context, repositoryRoot, sourcePNG, outputICO, outputICNS string) error {
	goBinary, err := exec.LookPath("go")
	if err != nil {
		return fmt.Errorf("未找到 Go 工具链: %w", err)
	}
	command := exec.CommandContext(ctx, goBinary,
		"tool", "wails3", "generate", "icons",
		"-input", sourcePNG,
		"-windowsfilename", outputICO,
		"-macfilename", outputICNS,
		"-sizes", "256,128,64,48,32,16",
	)
	command.Dir = repositoryRoot
	command.Env = replaceEnvironment(os.Environ(), "GOENV", "./go.env")
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("Wails 图标生成失败: %w\n%s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func syncIcons(ctx context.Context, repositoryRoot, brandDirectory string, generator iconGenerator) error {
	if err := verifyUniqueBrandFiles(repositoryRoot, brandDirectory); err != nil {
		return err
	}
	paths, err := validateBrandSources(brandDirectory)
	if err != nil {
		return err
	}
	generated, err := generateAndValidate(ctx, repositoryRoot, paths.png, generator)
	if err != nil {
		return err
	}
	if err := atomicWrite(paths.ico, generated.ico, 0o644); err != nil {
		return fmt.Errorf("写入 %s 失败: %w", paths.ico, err)
	}
	if err := atomicWrite(paths.icns, generated.icns, 0o644); err != nil {
		return fmt.Errorf("写入 %s 失败: %w", paths.icns, err)
	}
	return verifyUniqueBrandFiles(repositoryRoot, brandDirectory)
}

func verifyIcons(ctx context.Context, repositoryRoot, brandDirectory string, generator iconGenerator) error {
	paths, err := validateBrandSources(brandDirectory)
	if err != nil {
		return err
	}
	committedICO, err := os.ReadFile(paths.ico)
	if err != nil {
		return fmt.Errorf("读取 %s 失败: %w", paths.ico, err)
	}
	committedICNS, err := os.ReadFile(paths.icns)
	if err != nil {
		return fmt.Errorf("读取 %s 失败: %w", paths.icns, err)
	}
	if err := validateICO(committedICO); err != nil {
		return fmt.Errorf("ICO 校验失败: %w", err)
	}
	if err := validateICNS(committedICNS); err != nil {
		return fmt.Errorf("ICNS 校验失败: %w", err)
	}
	generated, err := generateAndValidate(ctx, repositoryRoot, paths.png, generator)
	if err != nil {
		return err
	}
	if !bytes.Equal(committedICO, generated.ico) {
		return errors.New("homestack.ico 与 PNG 不一致，请先人工确认并更新 PNG，或执行 icons sync")
	}
	if !bytes.Equal(committedICNS, generated.icns) {
		return errors.New("homestack.icns 与 PNG 不一致，请先人工确认并更新 PNG，或执行 icons sync")
	}
	return verifyUniqueBrandFiles(repositoryRoot, brandDirectory)
}

type brandPaths struct {
	svg  string
	png  string
	ico  string
	icns string
}

func validateBrandSources(brandDirectory string) (brandPaths, error) {
	paths := brandPaths{
		svg:  filepath.Join(brandDirectory, "homestack.svg"),
		png:  filepath.Join(brandDirectory, "homestack.png"),
		ico:  filepath.Join(brandDirectory, "homestack.ico"),
		icns: filepath.Join(brandDirectory, "homestack.icns"),
	}
	if err := validateSVG(paths.svg); err != nil {
		return brandPaths{}, err
	}
	if err := validatePNG(paths.png); err != nil {
		return brandPaths{}, err
	}
	return paths, nil
}

type generatedIcons struct {
	ico  []byte
	icns []byte
}

func generateAndValidate(ctx context.Context, repositoryRoot, sourcePNG string, generator iconGenerator) (generatedIcons, error) {
	temporaryDirectory, err := os.MkdirTemp("", "homestack-icons-")
	if err != nil {
		return generatedIcons{}, fmt.Errorf("创建图标临时目录失败: %w", err)
	}
	defer os.RemoveAll(temporaryDirectory)

	icoPath := filepath.Join(temporaryDirectory, "homestack.ico")
	icnsPath := filepath.Join(temporaryDirectory, "homestack.icns")
	if err := generator.Generate(ctx, repositoryRoot, sourcePNG, icoPath, icnsPath); err != nil {
		return generatedIcons{}, err
	}
	icoData, err := os.ReadFile(icoPath)
	if err != nil {
		return generatedIcons{}, fmt.Errorf("读取生成的 ICO 失败: %w", err)
	}
	icnsData, err := os.ReadFile(icnsPath)
	if err != nil {
		return generatedIcons{}, fmt.Errorf("读取生成的 ICNS 失败: %w", err)
	}
	if err := validateICO(icoData); err != nil {
		return generatedIcons{}, fmt.Errorf("生成的 ICO 无效: %w", err)
	}
	if err := validateICNS(icnsData); err != nil {
		return generatedIcons{}, fmt.Errorf("生成的 ICNS 无效: %w", err)
	}
	return generatedIcons{ico: icoData, icns: icnsData}, nil
}

func validateSVG(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("读取 %s 失败: %w", path, err)
	}
	defer file.Close()
	decoder := xml.NewDecoder(file)
	for {
		token, decodeErr := decoder.Token()
		if decodeErr != nil {
			if errors.Is(decodeErr, io.EOF) {
				return errors.New("SVG 缺少根元素")
			}
			return fmt.Errorf("解析 SVG 失败: %w", decodeErr)
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		if start.Name.Local != "svg" {
			return fmt.Errorf("SVG 根元素必须是 svg，实际为 %s", start.Name.Local)
		}
		viewBox := ""
		for _, attribute := range start.Attr {
			if attribute.Name.Local == "viewBox" {
				viewBox = attribute.Value
			}
		}
		if viewBox != "0 0 1024 1024" {
			return fmt.Errorf("SVG viewBox 必须是 0 0 1024 1024，实际为 %q", viewBox)
		}
		break
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("读取 SVG 配色失败: %w", err)
	}
	for _, expected := range []string{"#10B981", "#2563EB", "#F8FAFC"} {
		if !bytes.Contains(data, []byte(expected)) {
			return fmt.Errorf("SVG 缺少预期颜色 %s", expected)
		}
	}
	return nil
}

func validatePNG(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("读取 %s 失败: %w", path, err)
	}
	defer file.Close()
	decoded, err := png.Decode(file)
	if err != nil {
		return fmt.Errorf("解析 PNG 失败: %w", err)
	}
	if decoded.Bounds() != image.Rect(0, 0, 1024, 1024) {
		return fmt.Errorf("PNG 必须是 1024×1024，实际为 %v", decoded.Bounds())
	}
	if decoded.ColorModel() != color.NRGBAModel {
		return fmt.Errorf("PNG 必须使用 RGBA 色彩模型，实际为 %T", decoded.ColorModel())
	}
	samples := []struct {
		point    image.Point
		expected color.NRGBA
		label    string
	}{
		{image.Pt(0, 0), color.NRGBA{0, 0, 0, 0}, "透明画布"},
		{image.Pt(512, 100), color.NRGBA{248, 250, 252, 255}, "浅色底板"},
		{image.Pt(300, 300), color.NRGBA{16, 185, 129, 255}, "翡翠绿模块"},
		{image.Pt(700, 300), color.NRGBA{37, 99, 235, 255}, "钴蓝模块"},
	}
	for _, sample := range samples {
		actual := color.NRGBAModel.Convert(decoded.At(sample.point.X, sample.point.Y)).(color.NRGBA)
		if actual != sample.expected {
			return fmt.Errorf("PNG %s像素不匹配: %v 处为 %#v，预期 %#v", sample.label, sample.point, actual, sample.expected)
		}
	}
	return nil
}

func validateICO(data []byte) error {
	if len(data) < 6 || binary.LittleEndian.Uint16(data[0:2]) != 0 || binary.LittleEndian.Uint16(data[2:4]) != 1 {
		return errors.New("文件头不是 ICO")
	}
	count := int(binary.LittleEndian.Uint16(data[4:6]))
	if len(data) < 6+count*16 {
		return errors.New("ICO 目录不完整")
	}
	sizes := make([]int, 0, count)
	for index := 0; index < count; index++ {
		offset := 6 + index*16
		width, height := int(data[offset]), int(data[offset+1])
		if width == 0 {
			width = 256
		}
		if height == 0 {
			height = 256
		}
		if width != height {
			return fmt.Errorf("ICO 第 %d 帧不是正方形: %d×%d", index, width, height)
		}
		sizes = append(sizes, width)
	}
	sort.Ints(sizes)
	if fmt.Sprint(sizes) != fmt.Sprint(expectedICOSizes) {
		return fmt.Errorf("ICO 尺寸必须为 %v，实际为 %v", expectedICOSizes, sizes)
	}
	return nil
}

func validateICNS(data []byte) error {
	if len(data) < 8 || string(data[:4]) != "icns" {
		return errors.New("文件头不是 ICNS")
	}
	declared := int(binary.BigEndian.Uint32(data[4:8]))
	if declared != len(data) {
		return fmt.Errorf("ICNS 声明长度为 %d，实际为 %d", declared, len(data))
	}
	return nil
}

func verifyUniqueBrandFiles(repositoryRoot, brandDirectory string) error {
	wanted := map[string]bool{
		"homestack.svg":  true,
		"homestack.png":  true,
		"homestack.ico":  true,
		"homestack.icns": true,
	}
	canonical, err := filepath.Abs(brandDirectory)
	if err != nil {
		return fmt.Errorf("解析品牌目录失败: %w", err)
	}
	duplicates := make([]string, 0)
	err = filepath.WalkDir(repositoryRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() && shouldSkipDirectory(repositoryRoot, path, entry.Name()) {
			return filepath.SkipDir
		}
		if entry.IsDir() || !wanted[entry.Name()] {
			return nil
		}
		absolute, absErr := filepath.Abs(filepath.Dir(path))
		if absErr != nil {
			return absErr
		}
		if absolute != canonical {
			relative, relErr := filepath.Rel(repositoryRoot, path)
			if relErr != nil {
				return relErr
			}
			duplicates = append(duplicates, relative)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("扫描品牌资源失败: %w", err)
	}
	if len(duplicates) > 0 {
		sort.Strings(duplicates)
		return fmt.Errorf("品牌资源存在仓库重复副本: %s", strings.Join(duplicates, ", "))
	}
	return nil
}

func shouldSkipDirectory(repositoryRoot, path, name string) bool {
	if name == ".git" || name == "node_modules" || name == "bin" || name == "dist" {
		return true
	}
	relative, err := filepath.Rel(repositoryRoot, path)
	return err == nil && filepath.ToSlash(relative) == "internal/web/dist"
}

func atomicWrite(path string, data []byte, mode os.FileMode) (err error) {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".homestack-icon-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = os.Remove(temporaryPath)
	}()
	if _, err = temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err = temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err = temporary.Chmod(mode); err != nil {
		_ = temporary.Close()
		return err
	}
	if err = temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func replaceEnvironment(environment []string, key, value string) []string {
	prefix := key + "="
	result := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		if !strings.HasPrefix(entry, prefix) {
			result = append(result, entry)
		}
	}
	return append(result, prefix+value)
}
