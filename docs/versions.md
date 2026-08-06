# 固定版本

HomeStack v1 不跟随 `latest`，外部组件由用户确认后从官方发布页安装，应用只做版本检测。

| 组件 | 固定版本 | 用途 |
| --- | --- | --- |
| Wails | `v3.0.0-beta.4` | 桌面容器；当前仍是 Beta |
| `@wailsio/runtime` | `3.0.0-beta.1` | Wails 官方前端运行时发布版本 |
| Headscale | `v0.29.3` | 单一自托管 tailnet、协调与自有 DERP |
| Tailscale | `v1.102.2` | 官方 WireGuard 客户端 |
| Pocket ID | `v2.12.0` | Passkey OIDC |
| FileBrowser Quantum | `v0.3.5` | 文件索引、预览与只读 API |
| Jellyfin | `v10.11.11` | 媒体库与 FFmpeg 转码 |
| cc-connect | `v1.4.1` | 企业微信 WebSocket 与 Codex |

cc-connect `v1.4.1` 仓库没有实际 LICENSE 文件，因此本仓库不重新分发其二进制。所有组件均禁止由 HomeStack 静默安装或升级。
