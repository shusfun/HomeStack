# 验收清单

## 自动测试

```bash
go test -p=2 -parallel=2 ./...
pnpm --dir frontend exec vitest run --maxWorkers=2 --minWorkers=1
pnpm --dir frontend typecheck
pnpm --dir frontend build
```

必须覆盖邀请过期与重放、签名篡改、配置回退、设备密钥封装、路径穿越、只读权限、票据重放、模块密钥白名单和 cc-connect 用户白名单。

## 固定组件

- `headscale version` 精确为 `0.29.3`，`configtest` 和 `policy check --bypass` 成功。
- `tailscale version --json` 精确为 `1.102.2`。
- FileBrowser Quantum、Jellyfin、Pocket ID、cc-connect 分别精确为版本清单中的版本。
- 固定真实二进制缺失时直接判定契约验收失败，不用模拟实现替代。

把部署机上已替换占位符并通过权限检查的 Headscale 配置路径传入后，运行真实二进制契约测试：

```bash
HOMESTACK_CONTRACT_HEADSCALE_CONFIG=/etc/headscale/config.yaml task contract
```

缺少任一固定组件、版本不一致、环境变量缺失或 Headscale 配置/策略无效时，该任务必须失败。

## 网络

- 两台设备位于不同 NAT 网络时，`tailscale ping` 显示直连，HomeStack 显示“直连”。
- 人为制造无法打洞的网络后，只出现 `homestack` 自有 DERP，HomeStack 显示“自有中继”。
- `tailscale debug derp-map` 不含 Tailscale 公共 DERP 区域。
- 非本人设备访问 TCP `9443` 被 Headscale policy 拒绝；设备之间其他端口全部拒绝。
- VPS 只监听 TCP 443/8443/8444 和 UDP 3478；Headscale gRPC、FileBrowser、Jellyfin、cc-connect 不可从公网访问。

## 文件与媒体

- 浏览、预览和下载正常，大文件支持 Range 与断点续传。
- 上传、重命名、删除、修改和 `..`、双重编码路径穿越均返回真实拒绝原因。
- 浏览器原生 MP4、MKV 按需转码、字幕、拖动跳转和 HLS 分片正常。
- 媒体响应直接来自设备尾网地址，VPS Control 没有业务流量。

## 企业微信

- WebSocket 连接成功，不存在公网回调监听。
- 白名单用户可用，未授权用户被拒绝，`allow_from/admin_from="*"` 无法创建邀请。
- 每个项目使用独立绝对 `work_dir`，项目之间不可切换目录。
- Codex 固定为 `suggest`，通过 stdio `app_server` 执行真实工具审批；Management API 没有监听端口。

## 平台

- Windows、macOS、Linux 分别完成 Wails 构建、Tailscale 入网、系统安全存储和 Agent 服务测试。
- 手机仅安装官方 Tailscale，响应式网页完成登录、设备列表、文件和媒体访问。
- 默认验收不运行 Playwright 或其他浏览器自动化；需要时必须另行明确授权。
