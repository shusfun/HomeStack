# Release

Release 由 GitHub Actions 在六个平台/架构构建，运行 Go、Vitest、typecheck、前端构建和清单契约测试。Control、Agent、桌面安装包和更新包均由固定 Ed25519 发布密钥签名。

自动更新通过固定公开加速地址读取 GitHub Release，且只接受 HomeStack Release 资产，并验证：

- 标签、二进制版本、操作系统和架构一致
- 归档结构符合固定清单
- SHA-256 摘要和 Ed25519 签名有效

Control 归档包含 Control、受限 Config Helper 和固定 systemd unit。桌面安装包和更新包包含 App 与同平台 Node sidecar。安装器不会修改 DNS、宝塔、TLS 或第三方 OAuth 控制台。

从 `v0.2.20` 开始，Control Owner 页面提供 VPS 更新入口。更新资产同时包含 Control 与 Config Helper；两层校验版本、平台、架构、SHA-256 和 Ed25519 签名后才替换程序，健康检查失败会恢复两个旧二进制。`v0.2.19` 及更早版本没有该入口，必须先使用官方 Control 安装包手动升级到 `v0.2.20` 一次，后续版本才能在网页中更新。

发布标签：

```bash
git tag v0.2.27
git push origin main v0.2.27
```

只有完整 GitHub Release 成功后，才允许在服务器使用带标签的官方安装器部署。
