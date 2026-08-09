# Release

Release 由 GitHub Actions 在六个平台/架构构建，运行 Go、Vitest、typecheck、前端构建和清单契约测试。Control、Agent、桌面安装包和更新包均由固定 Ed25519 发布密钥签名。

安装器只接受 GitHub Release 资产，并验证：

- 标签、二进制版本、操作系统和架构一致
- 归档结构符合固定清单
- SHA-256 摘要和 Ed25519 签名有效

Control 归档包含 Control、受限 Config Helper 和固定 systemd unit。桌面安装包和更新包包含 App 与同平台 Node sidecar。安装器不会修改 DNS、宝塔、TLS 或第三方 OAuth 控制台。

发布标签：

```bash
git tag v0.2.5
git push origin main v0.2.5
```

只有完整 GitHub Release 成功后，才允许在服务器使用带标签的官方安装器部署。
