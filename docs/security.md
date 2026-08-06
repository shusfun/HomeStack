# 安全模型

## 信任边界

- 公网 VPS 可验证身份、登记设备、签发短期票据和下发签名配置，但不持有设备 X25519 私钥。
- Tailscale/WireGuard 负责数据面加密。文件、下载、媒体和 Agent 页面不经过 HomeStack Control。
- 无法点对点连接时仅允许 Headscale 内置的自有 DERP；`derp.urls` 为空且自动更新关闭。
- HomeStack 不是代理服务，不提供出口节点，也不会生成“小火箭节点”。Headscale 策略没有设备互访、子网路由或互联网出口授权。

## 身份与配置

- Pocket ID 通过 Passkey 提供 OIDC，授权码流程使用 `state`、`nonce` 和 PKCE S256。
- 桌面端 OIDC 唯一允许的 HTTP 是 RFC 8252 本机回环回调 `http://127.0.0.1:<随机端口>/callback`，不会离开设备。
- 邀请码使用安全随机数，十分钟过期且只能兑换一次；Headscale 预认证密钥同样十分钟过期、单次使用。
- Control 使用 Ed25519 JWS 签名设备配置。Agent 校验版本、设备 ID、时间和单调递增 revision，拒绝篡改、过期和回退。
- 设备凭据使用临时 X25519 密钥协商、HKDF-SHA256 和 AES-256-GCM 封装。
- 设备私钥与档案仅进入 macOS Keychain、Windows Credential Manager 或 Linux Secret Service；安全存储不可用时直接失败。

## 访问控制

- Headscale 从单条 owner-only grant 开始：已登录用户仅能访问自己名下设备的 TCP `9443`。
- Control 签发三十秒单次访问票据；Agent 持久化票据 nonce 防重放，再签发 `Secure`、`HttpOnly`、`SameSite=Strict` 会话。
- Agent 只接受 Tailscale 地址上的 HTTPS。生产证书必须可由客户端系统信任，禁止忽略证书错误。
- FileBrowser 只允许 GET/HEAD 的资源、原始文件、预览、搜索和用量接口；上传、重命名、修改、删除和路径穿越均在 BFF 拒绝。
- Jellyfin 只代理媒体浏览、图片、播放信息、Range/HLS 和播放进度相关接口。
- cc-connect 强制企业微信 WebSocket、明确 `allow_from/admin_from`，拒绝 `*`，按项目限制绝对 `work_dir`。Codex 固定为 `suggest` 与 stdio `app_server`，工具调用必须经过真实审批；不启用 Management API，也不监听 app-server 端口。

## 已知边界

- 单台自有 DERP 是单点故障；故障时不降级到公共 DERP。
- Wails 3 固定版本仍为 Beta，发布前必须分别完成 Windows、macOS、Linux 构建验收。
- FileBrowser Quantum `v0.3.5` 的监听实现忽略配置中的地址并绑定所有接口。部署单元通过 systemd `IPAddressDeny=any` 与 `IPAddressAllow=localhost` 在内核层只允许本机流量；不支持该 systemd 能力的平台不得直接启动该组件。
- Linux Agent 依赖当前用户的 Secret Service。没有可解锁安全存储的纯 headless 会话会直接启动失败，不会写明文密钥。
