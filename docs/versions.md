# 固定组件

| 组件 | 固定版本 | 用途 |
| --- | --- | --- |
| HomeStack | `0.2.18` | Control、App 与 Node |
| Tailscale | `1.102.2` | 官方 Tailnet 与 Serve |
| Wails | `3.0.0-beta.4` | 桌面应用 |
| FileBrowser Quantum | `1.5.1-stable` | HomeStack 托管多目录只读文件服务 |
| Jellyfin | `10.11.11` | HomeStack 托管媒体服务 |
| Jellyfin FFmpeg | `7.1.4-3` | HomeStack 托管转码、HLS 与拖动跳转 |
| cc-connect | `1.4.1` | Linux 可选连接服务 |

FileBrowser Quantum 与 Jellyfin FFmpeg 通过签名组件清单声明的公开 GitHub 加速候选源下载，Jellyfin 使用官方仓库。清单固定最终平台资产的架构、大小与 SHA-256；客户端下载不经过 Control VPS。
Windows ARM64 通过 Windows 11 ARM 的 x64 应用兼容层运行 FileBrowser Quantum 官方 Windows x64 资产；其余组件使用对应原生架构资产。

Control VPS 不安装 Tailscale 或任何额外身份、协调、文件代理服务。
