# HomeStack 品牌资源

本目录是仓库内 HomeStack 品牌资源的唯一位置：

- `homestack.svg`：唯一可编辑设计源。
- `homestack.png`：人工确认后的 1024×1024 RGBA 图像。
- `homestack.ico`、`homestack.icns`：由仓库 CLI 从 PNG 生成的平台资源。

## SVG 转 PNG

该步骤是人工设计确认点，不属于资产 CLI。修改 SVG 后运行固定命令：

```bash
ffmpeg -hide_banner -loglevel error -y \
  -i assets/brand/homestack.svg \
  -vf "scale=1024:1024:flags=lanczos,format=rgba" \
  -frames:v 1 assets/brand/homestack.png
```

若 FFmpeg 返回缺少 SVG 解码器，应直接更换为支持 SVG 输入的 FFmpeg 环境；不得由 CLI 自动改用其他转换路径。

检查 PNG 后再同步平台图标：

```bash
GOENV=./go.env go tool wails3 task assets:icons:sync -C 2
GOENV=./go.env go tool wails3 task assets:icons:verify -C 2
```

等价的底层命令为：

```bash
GOENV=./go.env go run ./cmd/homestack-assets icons sync
GOENV=./go.env go run ./cmd/homestack-assets icons verify
```

CLI 不会生成或改写 SVG、PNG，也不接受自定义输出目录。后续资源能力通过
`homestack-assets <资源组> <动作>` 增加，现有 `icons sync/verify` 接口保持不变。
