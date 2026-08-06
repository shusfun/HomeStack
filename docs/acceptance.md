# 验收清单

## 自动测试

```bash
go test -p=2 -parallel=2 ./...
pnpm --dir frontend exec vitest run --maxWorkers=2 --minWorkers=1
pnpm --dir frontend typecheck
pnpm --dir frontend build
bash -n deploy/install.sh
bash -n scripts/package-release.sh
```

必须覆盖：三类身份提供商配置，state/nonce/PKCE，首个所有者、显式绑定和同邮箱不合并；App code 单次兑换和会话过期；十分钟配对码重放；三十秒票据、错误所有者、固定跳转和 Origin；Agent API `401`、Helper 白名单、日志边界、资源指标、媒体 Range/进度；更新签名篡改、非法归档及错误版本/平台/架构。

## 网络

- 不同 NAT 下 `tailscale ping` 优先显示直连；无法打洞时只出现 `homestack` 自有 DERP。
- `tailscale debug derp-map` 不含公共 DERP。
- Agent 只在 Tailnet IP 的 TCP `9443` 监听；VPS 不监听或转发文件、媒体、日志端口。
- 打开设备后浏览器地址栏停留在 Agent Tailnet HTTPS 域名，下载或播放期间 Control 流量不增长。
- 非本人设备和其他端口由 Headscale policy 拒绝。

## 文件、影视与管理

- 浏览、预览、下载、大文件 Range、拖动播放、HLS、图片和播放进度正常。
- 上传、重命名、删除、修改及单次/双重编码路径穿越返回真实拒绝原因。
- CPU、内存、磁盘和网络监控可用；Helper Socket 只接受 Agent UID。
- Tailscale/Agent 只有重启，FileBrowser/Jellyfin 只有启停/重启；任意 unit、参数、命令和日志表达式被拒绝。
- 日志限制 1 到 500 行，非法 cursor 被拒绝，敏感字段不出现在响应。

## 更新与平台

- `latest.json` 完整包含 10 个更新项，desktop 项优先，所有资产签名与内嵌版本一致。
- 签名缺失或篡改、平台/架构/版本不匹配、非法归档、下载中断、替换或重启失败均停止安装并显示真实错误。
- Agent 新版本健康失败时恢复旧版本并再次健康检查；失败程序被保留用于排查。
- macOS、Windows、Linux 使用各自原生 runner；macOS 不启动 Docker。
- `.dmg`、NSIS、Portable zip、AppImage、deb、CLI tar 和更新载荷均核对名称与单顶层结构。

## 固定组件

- Headscale `0.29.3`、Tailscale `1.102.2`、Pocket ID `2.12.0`、FileBrowser Quantum `0.3.5`、Jellyfin `10.11.11`、cc-connect `1.4.1`。
- 固定真实二进制缺失或版本不同直接失败，不使用模拟实现或默认规则降级。

默认验收不运行 Playwright、浏览器自动化或 Docker；只有得到明确授权后才可使用。
