# HomeStack

HomeStack 使用官方 Tailscale 把 macOS、Windows、Linux 和手机连接到同一 Tailnet。公网 VPS 只运行 HomeStack Control；宝塔只需把一个域名反向代理到 `http://127.0.0.1:18443`。

## 架构

- Control 负责 Google 或 GitHub OAuth、唯一 Owner、设备登记、心跳和 30 秒单次访问票据。
- 桌面 App 同时是管理客户端和受管 Node；Linux headless 使用相同 Node 核心。
- Node 只监听 `127.0.0.1:19444`，由 Tailscale Serve 暴露为 `https://<MagicDNS>:19443`。
- 文件、状态和媒体流量由浏览器通过 Tailnet 直连 Node，不经过 VPS。
- HomeStack 从不执行 `tailscale up`，用户需先自行安装并登录官方 Tailscale。

## 开发

```bash
pnpm install --frozen-lockfile
pnpm --dir frontend test
pnpm --dir frontend typecheck
go test -p=2 -parallel=2 ./...
```

不使用 Docker。版本、依赖和 Release 资产由仓库锁文件、CI 与签名清单共同约束。

## 部署

Control 必须通过 GitHub Release 的签名安装器安装。首次启动会监听 `127.0.0.1:18443` 并输出一次性 Setup 令牌；Setup 只填写 VPS 域名、选择 Google 或 GitHub，再填写对应 OAuth Client ID/Secret。

完整说明见 [docs/deployment.md](docs/deployment.md)、[docs/security.md](docs/security.md) 和 [docs/release.md](docs/release.md)。
