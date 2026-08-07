# HomeStack

HomeStack 用官方 Tailscale 连接桌面、手机和 Linux 设备。公网 VPS 只运行 Control、Headscale 和自有 DERP：登录、设备登记、心跳、配对码和访问票据经过 Control；文件、影视、日志和设备管理由浏览器直接访问 Agent Tailnet HTTPS 地址，优先 WireGuard 点对点，无法打洞时只经过自有 DERP。

首版范围：

- Windows、macOS、Linux 极简 Wails App，只负责登录、Tailnet 状态、设备列表、Linux 配对和用默认浏览器打开设备。
- 单人所有者模型，支持 Pocket ID OIDC Passkey、Google OIDC 和 GitHub OAuth；同邮箱不会自动合并身份。
- Linux Agent 网页提供状态、文件、Jellyfin 日常播放、资源监控、固定服务、受限日志和签名更新。
- 不提供写文件、任意命令、Web Shell、远程桌面、出口节点、子网路由或代理节点。

## 仓库结构

```text
cmd/homestack-desktop  极简 Wails 设备连接器
cmd/homestack-agent    Linux 设备 BFF 与事务更新器
cmd/homestack-control  公网身份与控制服务
cmd/homestack-helper   Linux 最小特权 systemd helper
cmd/homestack-setup-helper  一次性 Setup 与受限维护 helper
internal/              协议、安全、认证与领域代码
frontend/              desktop/control/agent 三个独立界面入口
deploy/                systemd、Headscale 和组件配置样例
docs/                  部署、安全、发布与验收文档
```

## 本地开发

要求 Node `26.6.0`、pnpm `10.32.1`、Go `1.26.1`。仓库固定 Go 与 npm 下载源，不做失败后的隐式源降级。

```bash
pnpm install --frozen-lockfile
go mod download
GOENV=./go.env go tool wails3 task dev
```

Wails 开发模式仅在本机回环地址启动 Vite。生产 Control 和 Agent 均要求受信任 HTTPS；不要把 Agent `9443` 暴露到公网。

## Linux 安装

安装器直接从 GitHub Release 下载，自行计算 SHA-256，并验证 `Ed25519(Sign(SHA-256(asset)))`。更新公钥必须通过可信渠道取得。

```bash
UPDATE_PUBLIC_KEY='REPLACE_WITH_BASE64_ED25519_PUBLIC_KEY'
curl -fsSL https://raw.githubusercontent.com/shusfun/HomeStack/main/deploy/install.sh \
  | sudo bash -s -- control --update-public-key "$UPDATE_PUBLIC_KEY"

curl -fsSL https://raw.githubusercontent.com/shusfun/HomeStack/main/deploy/install.sh \
  | bash -s -- agent --update-public-key "$UPDATE_PUBLIC_KEY"
```

Control 以 root 安装。首次安装会启动 `127.0.0.1:8443` Setup 并输出一次性令牌；Setup Helper 从官方 Release 固定下载并校验 Headscale `0.29.3` 与 Pocket ID `2.12.0`，完成后永久关闭安装能力。宝塔分别反代 Control `127.0.0.1:8443`、Pocket ID `127.0.0.1:8444` 和 Headscale `127.0.0.1:18080`。

Agent 脚本必须以最终普通用户运行，并仅在安装固定 root helper、启用 linger 和设置 Tailscale operator 时调用 `sudo`。安装器不会安装或升级 Tailscale、FileBrowser、Jellyfin 或 cc-connect。

## 连接流程

1. 第一个成功登录 Control 的身份成为唯一所有者；公网首次开放后必须立即完成认领。
2. App 使用系统浏览器和回环 PKCE 登录，通过官方 Tailscale 客户端加入 Tailnet。
3. App 或 Control 生成十分钟单次 Linux 配对命令，Agent 将密封档案写入 `systemd-creds`。
4. 打开设备时 App 向 Control 领取三十秒单次票据，再让系统默认浏览器访问 Agent `/access`；最终地址栏停留在设备 Tailnet 域名。

部署见 [docs/deployment.md](docs/deployment.md)，安全边界见 [docs/security.md](docs/security.md)，发布资产见 [docs/release.md](docs/release.md)。
