# 原生部署

HomeStack Control 通过 GitHub Release 原生安装，不使用 Docker。首次安装会启动一次性 Setup，由 Setup Helper 安装并固定校验 Headscale `0.29.3` 与 Pocket ID `2.12.0`；任何下载、摘要或配置检查失败都会保留 Setup 状态并返回真实错误。

## 1. DNS、端口与宝塔

先为同一台 VPS 配置四个不重复的直连 DNS 记录。Control、Pocket ID、Headscale 的 DNS 必须直接解析到 VPS 公网 IPv4，不得启用 Cloudflare Proxy、Tunnel 或其他中间代理。

宝塔负责公网 TLS，并配置以下 HTTP 反向代理：

| 公网域名 | 宝塔上游 | 说明 |
| --- | --- | --- |
| `app.example.com` | `http://127.0.0.1:8443` | HomeStack Control 与首次 Setup |
| `id.example.com` | `http://127.0.0.1:8444` | Pocket ID |
| `mesh.example.com` | `http://127.0.0.1:18080` | Headscale HTTP 与 WebSocket |

Headscale 站点必须透传 `Upgrade`/`Connection` WebSocket 请求头并关闭代理缓冲。`127.0.0.1:50443` 是 Headscale gRPC，只供本机 Control 使用，不配置宝塔反代。

公网防火墙只开放 TCP `80/443` 和 UDP `3478`。不得开放 TCP `8443`、`8444`、`18080`、`50443`。Tailnet 基础域名（例如 `tail.example.com`）用于设备名称，不作为 VPS 后端端口。

## 2. 安装与 Setup

使用 Release 验签公钥运行官方安装器：

```bash
UPDATE_PUBLIC_KEY='REPLACE_WITH_BASE64_ED25519_PUBLIC_KEY'
curl -fsSL https://raw.githubusercontent.com/shusfun/HomeStack/v0.1.8/deploy/install.sh | \
  sudo bash -s -- control --version v0.1.8 --update-public-key "$UPDATE_PUBLIC_KEY"
```

安装器会校验 Control Release 的 Ed25519 签名、内嵌版本和架构，保留已有 Control 签名密钥，并在首次安装时输出 256 位一次性 Setup 令牌。令牌只保存 SHA-256 摘要，首次成功兑换后立即删除；Setup 会话固定有效 24 小时且重启后仍不可重复兑换令牌。

将宝塔 Control 站点指向 `http://127.0.0.1:8443`，打开 `https://app.example.com/setup` 并输入安装器输出的令牌。Setup 页面依次完成：

1. 校验四个域名和 VPS 公网 IPv4。
2. 安装并启动 Pocket ID，在 Pocket ID 原生 `/setup` 创建首个 Passkey 管理员。
3. 创建受限用户组及 HomeStack、Headscale 两个 confidential PKCE S256 OIDC 客户端。
4. 校验 Headscale policy/config 和 Control 配置，再切换到正式服务。

Setup 完成后安装接口永久返回 `423 setup_locked`，一次性 Setup Helper 停止，权限更窄的 Maintenance Helper 启动。首次通过 Pocket ID 登录 Control 的用户认领唯一 Owner。

Setup 未完成且令牌确实丢失时，root 可显式重置：

```bash
curl -fsSL https://raw.githubusercontent.com/shusfun/HomeStack/v0.1.8/deploy/install.sh | \
  sudo bash -s -- upgrade control --version v0.1.8 --reset-setup-token \
  --update-public-key "$UPDATE_PUBLIC_KEY"
```

完成标记存在后该参数会被永久拒绝。

## 3. 域名与公网 IP 迁移

不需要重新安装。在 Control 的“设置 -> 域名与网络”提交完整五项配置。提交前必须：

1. 先在 DNS、宝塔和证书中配置新域名，并确认新域名已反代到当前 VPS。
2. 使用 Pocket ID Passkey 重新认证当前 Owner；授权绑定当前浏览器会话，五分钟过期且单次使用。
3. 输入新的 Control 域名作为确认文本。

迁移期间 Maintenance Helper 会保留新旧 OIDC 回调，结构化更新 Headscale YAML，逐项重启并健康检查。成功后删除旧回调和临时 Pocket API Key，浏览器跳转到新 Control 域名重新登录；失败时恢复配置、回调和服务并返回原始错误及回滚错误。

存在已登记设备时禁止修改 Tailnet 基础域名；Control、Pocket ID、Headscale 域名和 VPS 公网 IPv4可独立迁移。Owner 始终按稳定的 `provider + subject` 识别，不会重新认领。

## 4. 设备端

设备端继续使用 Release 安装器安装 Agent。以最终用户运行，不能对整个 Agent 安装器使用 sudo：

```bash
curl -fsSL https://raw.githubusercontent.com/shusfun/HomeStack/v0.1.8/deploy/install.sh | \
  bash -s -- agent --version v0.1.8 --update-public-key "$UPDATE_PUBLIC_KEY"
```

Agent 只在设备 Tailnet IP 的 TCP `9443` 提供 HTTPS。FileBrowser、Jellyfin 和管理端口不得暴露到公网。完整验证项目见 [acceptance.md](acceptance.md)。
