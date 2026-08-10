# 固定组件

| 组件 | 固定版本 | 用途 |
| --- | --- | --- |
| HomeStack | `0.2.12` | Control、App 与 Node |
| Tailscale | `1.102.2` | 官方 Tailnet 与 Serve |
| Wails | `3.0.0-beta.4` | 桌面应用 |
| FileBrowser Quantum | `0.3.5` | HomeStack 托管只读文件服务 |
| Jellyfin | `10.11.11` | HomeStack 托管媒体服务 |
| Jellyfin FFmpeg | `7.1.4-3` | HomeStack 托管转码、HLS 与拖动跳转 |
| UPX | `3.96` | 发布阶段解压同版本打包的 macOS FileBrowser 上游资产 |
| cc-connect | `1.4.1` | Linux 可选连接服务 |

FileBrowser Quantum、Jellyfin 与 Jellyfin FFmpeg 的固定上游资产由 HomeStack Release 镜像；macOS FileBrowser 在发布阶段使用与上游打包版本一致且摘要固定的 UPX 解压，以兼容系统执行策略。签名组件清单固定最终平台资产的架构、大小与 SHA-256；客户端下载不经过 Control VPS。
Windows ARM64 通过 Windows 11 ARM 的 x64 应用兼容层运行 FileBrowser Quantum 官方 Windows x64 资产；其余组件使用对应原生架构资产。

Control VPS 不安装 Tailscale 或任何额外身份、协调、文件代理服务。
