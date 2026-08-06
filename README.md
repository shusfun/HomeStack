# HomeStack

HomeStack 是一个 Wails 3 多端 NAS 与远程开发整合器。公网 VPS 只承担 Passkey 身份、设备注册、签名配置和 Headscale 协调；文件、下载和媒体流优先通过 Tailscale/WireGuard 点对点传输，无法直连时仅使用自有 DERP。

首版范围：

- Windows、macOS、Linux Wails 桌面端，粘贴一次性连接信息后加入。
- 响应式 Control 与 Agent 网页，手机通过官方 Tailscale 客户端访问。
- 设备状态、只读文件浏览/下载、Jellyfin 媒体和企业微信 cc-connect。
- 不包含写文件、远程终端、远程桌面、出口节点或代理节点。

## 仓库结构

```text
cmd/homestack-desktop  Wails 桌面端
cmd/homestack-agent    设备端只读 BFF
cmd/homestack-control  公网身份与控制服务
internal/              共用协议、安全与领域代码
frontend/              React/TypeScript/Tailwind 单一前端
deploy/                systemd、Headscale 和组件配置样例
docs/                  部署、安全模型与验收清单
```

## 本地开发

要求 Node `26.6.0`、pnpm `10.32.1`、Go `1.26.1`。仓库固定 `GOPROXY=https://goproxy.cn` 和 `registry=https://registry.npmmirror.com/`，没有下载源降级。

```bash
pnpm install --frozen-lockfile
go mod download
GOENV=./go.env go tool wails3 task dev
```

Wails 开发模式会在本机回环地址启动 Vite；这只用于本机开发，不是生产服务。生产 Control、Pocket ID、Headscale 和 Agent 均要求 HTTPS。

部署从 [docs/deployment.md](docs/deployment.md) 开始，固定组件版本见 [docs/versions.md](docs/versions.md)，威胁边界见 [docs/security.md](docs/security.md)。

## Linux 一键安装

安装脚本从 GitHub Release 下载固定产物并强制验证 `checksums.txt`。它只安装 HomeStack 自身，不会安装或升级 Headscale、Pocket ID、Tailscale、FileBrowser、Jellyfin 或 cc-connect。

Control 必须以 root 安装：

```bash
curl -fsSL https://raw.githubusercontent.com/shusfun/HomeStack/main/deploy/install.sh | sudo bash -s -- control
```

Agent 必须以最终使用者身份安装，不得使用 `sudo`：

```bash
curl -fsSL https://raw.githubusercontent.com/shusfun/HomeStack/main/deploy/install.sh | bash -s -- agent
```

安装指定版本或升级现有服务：

```bash
curl -fsSL https://raw.githubusercontent.com/shusfun/HomeStack/main/deploy/install.sh | sudo bash -s -- upgrade control --version v0.1.1
curl -fsSL https://raw.githubusercontent.com/shusfun/HomeStack/main/deploy/install.sh | bash -s -- upgrade agent --version v0.1.1
```

脚本不会启动尚未完成 HTTPS、身份或设备档案配置的全新服务。Release 与本地打包说明见 [docs/release.md](docs/release.md)。

## 连接流程

管理员在 Control 生成以下一次性连接信息：

```text
homestack://join?server=https://app.example.com:8443&code=<一次性随机码>
```

桌面端粘贴并点击“连接”，随后完成 Pocket ID Passkey 登录。邀请码十分钟失效且只能兑换一次，不携带长期密钥。设备密钥保存在系统安全存储中，不提供明文文件降级。
