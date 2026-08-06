# 安全模型

## 网络与信任边界

- VPS Control 只处理登录、设备登记、心跳、签名配置、十分钟配对码和三十秒访问票据，不代理 Agent 页面或业务流量。
- 文件、影视、日志和管理请求直接到设备 Tailnet HTTPS 地址；Tailscale 优先 WireGuard 点对点，失败时仅使用 Headscale 配置的自有 DERP。
- Agent 上游 FileBrowser、Jellyfin 和 cc-connect 只监听回环地址，Agent `9443` 只绑定 Tailscale IP。
- 不提供公共 DERP、出口节点、子网路由、反向代理、任意命令、Web Shell 或系统升级入口。

## 所有者与登录

- 系统只持久化一个 `Owner`，一个所有者可绑定多个 `{provider, subject}` 身份键。
- 第一个成功登录者认领所有者；此后未绑定身份一律拒绝，即使邮箱完全相同也不会自动合并。
- Pocket ID 和 Google 使用 OIDC，GitHub 使用 OAuth；授权流程均使用一次性 `state` 和 PKCE S256，OIDC 额外验证 ID Token `nonce`。
- 新身份只能由已有浏览器会话的所有者从“登录身份”页面主动绑定。
- App 登录使用系统浏览器和随机回环端口。回环只接收两分钟单次 code，Control 再验证 App PKCE verifier 后签发 access/refresh token。
- Control 只持久化会话令牌 SHA-256；App refresh token 只进入 macOS Keychain、Windows Credential Manager 或 Linux Secret Service。

首个登录即所有者存在公网抢占窗口。首次部署必须在 Control 对公网开放后立即完成所有者登录。

## 配对与访问

- Linux 配对码使用安全随机数，十分钟过期且只能兑换一次；Headscale 预认证密钥同样短期、单次使用。
- Agent 生成临时 X25519 密钥，Control 用 HKDF-SHA256 与 AES-256-GCM 密封设备凭据，并用 Ed25519 JWS 签名设备配置。
- Agent 校验配置设备 ID、域名、过期时间和单调 revision；设备档案与 TLS 私钥通过 `systemd-creds` 注入。
- Control 只对当前所有者名下的固定设备签发三十秒票据，不接受任意 return URL。Agent 持久化 nonce 防重放，再签发 `Secure`、`HttpOnly`、`SameSite=Strict` 会话。
- 未登录的 Agent 文档请求跳到 Control 固定设备入口；API 请求返回真实 `401`。浏览器写操作必须通过同源 Origin 校验。

## Agent 权限

- FileBrowser 只允许 GET/HEAD 的资源、原始文件、预览、搜索和用量接口；写操作和路径穿越直接拒绝。
- Jellyfin 只代理浏览、图片、Range/HLS、播放信息和播放进度白名单。
- root helper 只接受指定 Agent UID 的 Unix Socket peer credential。
- `tailscale` 和 `homestack-agent` 只允许重启；`filebrowser`、`jellyfin` 只允许启停或重启。服务 ID、动作和参数均为固定值。
- 日志只允许固定 unit、1 到 500 行和无空白的 journal cursor，并对 authorization、token、secret、password、cookie 字段脱敏。
- cc-connect 生命周期由 Agent 内部管理，禁止把任意 systemd unit 或命令传给 helper。

## 更新信任

- 桌面端和 Agent 直接从 GitHub Release 下载，不经过 VPS。
- CI 私钥签名资产 SHA-256 digest；客户端只内置 Ed25519 公钥。缺签名、摘要、资产、平台、架构或内嵌版本不匹配都会失败。
- Agent 更新在同文件系统暂存，严格解包单文件，核对 `--version-json`，备份重命名后由独立 helper 重启并健康检查；失败恢复旧版本并再次健康检查。
- `checksums.txt` 只供人工核验，不作为更新信任根。

## 已知边界

- 单台自有 DERP 是单点故障，故障时不会降级到公共 DERP。
- Wails 3 当前固定 Beta 版本，必须在三种操作系统原生 runner 验证。
- macOS 首版仅 ad-hoc codesign 且未公证，Windows 未做 Authenticode，系统可能显示 Gatekeeper 或 SmartScreen 警告。
- FileBrowser Quantum `v0.3.5` 仍依赖 systemd 网络沙箱把监听限制在本机；缺少该能力的平台不得直接启动。
