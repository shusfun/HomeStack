# Release 与一键安装

## 发布契约

GitHub Actions 在推送符合 `vX.Y.Z` 的已有标签时创建 Release，也允许手动选择一个已经存在的标签。工作流固定使用 Go `1.26.1`、Node `26.6.0` 和 pnpm `10.32.1`，先完成 Go 测试、前端测试、类型检查与生产构建，再生成发布产物。

Release 包含：

- Linux amd64/arm64：`homestack-control` 与 `homestack-agent`。
- Windows amd64/arm64：Wails 桌面端可执行文件归档。
- macOS Intel/Apple Silicon：临时签名的 `.app` 归档。
- Linux amd64/arm64：依赖 GTK4 与 WebKitGTK 6 的桌面端归档。
- `checksums.txt`：所有归档的 SHA-256。

当前桌面包没有商业代码签名或公证。Windows SmartScreen 与 macOS Gatekeeper 可能显示来源提示；正式对外分发前必须配置独立签名凭据，不能关闭系统安全检查作为替代方案。

## 创建发布

先确保默认分支测试通过，再创建带说明的标签：

```bash
git tag -a v0.1.0 -m "HomeStack v0.1.0"
git push origin v0.1.0
```

工作流不创建标签，也不会覆盖已有 Release。任何矩阵任务失败时均不会发布部分产物。

## 本地打包

通过 Wails 内置的 Task 运行器调用同一脚本：

```bash
GOENV=./go.env go tool wails3 task release:package \
  COMPONENT=control VERSION=v0.1.0 GOOS=linux ARCH=amd64
```

`COMPONENT` 只能是 `control`、`agent` 或 `desktop`。Control 与 Agent 只允许 Linux 目标；桌面端必须在对应操作系统原生构建。

## 安装边界

安装器支持 `install`、`upgrade` 和 `--version`，每次下载都要求 Release 同时存在匹配的 `checksums.txt` 条目。配置文件已存在时不会覆盖；升级运行中的服务时会先停止，二进制校验并替换成功后再启动。

安装器不会执行以下操作：

- 安装或升级任何外部组件。
- 自动申请域名或 TLS 证书。
- 把 HTTPS 降级成 HTTP。
- 开启公共 DERP、出口节点或代理节点。
- 在 Agent 安全存储不可用时改用明文密钥。
