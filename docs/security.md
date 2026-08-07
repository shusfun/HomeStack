# 安全模型

- VPS 只监听回环 Control 端口，公网 TLS 由宝塔处理。
- Setup 至少配置 Google 或 GitHub 中一个；Owner 可在重新认证后绑定第二种，身份固定使用 `provider + subject`，不按邮箱合并。
- Setup 令牌和设备激活码均为 256 位随机值，服务端只持久化 SHA-256 摘要，并在首次成功兑换时消费。
- App 会话和 Node 机器凭据分开签发、分开保存。桌面密钥进入系统 keyring，Linux headless 进入 `systemd-creds`，没有明文降级。
- Node 只读取已登录的 Tailscale 状态，从 MagicDNS 推导唯一 HTTPS 地址；从不执行 `tailscale up`。
- Tailscale Serve 配置修改前读取现状；端口冲突或 Funnel 启用时直接失败。
- Control 签发的设备访问票据 30 秒过期且只能兑换一次。设备内容不会经过 VPS。
- 原生文件服务只读取明确配置的规范化根目录，拒绝写操作、路径穿越、符号链接越界和特殊设备文件。
- Linux Helper 通过 Unix Socket peer UID 鉴权，只提供固定服务、资源指标和脱敏日志；没有 Tailscale 服务控制权限。
- 所有错误返回真实原因，不做 HTTP、公网监听、明文凭据或默认配置降级。
