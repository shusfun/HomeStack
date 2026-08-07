# 部署

## VPS

VPS 只运行 HomeStack Control，不安装 Tailscale。公网仅开放 TCP `80/443`，Control 仅监听 `127.0.0.1:18443`。

宝塔只配置一个站点：

```text
https://home.example.com -> http://127.0.0.1:18443
```

DNS 必须直接解析到 VPS，TLS 由宝塔终止。HomeStack 不写入或重载宝塔配置。

首次安装后访问 `/setup`，输入安装器输出的一次性令牌，然后填写：

- VPS 域名，例如 `home.example.com`
- Google 或 GitHub，二选一
- OAuth Client ID 和 Client Secret

回调地址固定为 `https://<VPS域名>/auth/callback/<provider>`。Setup 完成后安装接口永久锁定，第一个成功 OAuth 登录者认领唯一 Owner。

## 设备

所有设备先自行安装并登录同一官方 Tailscale Tailnet。HomeStack 不修改 Tailnet 登录状态。

Owner 可在 Control 生成十分钟单次激活码。桌面 App 填写 VPS 域名后可直接 OAuth 登录，或使用激活码；两种方式都会登记本机 Node。Linux headless 使用：

```bash
homestack-agent activate --server https://home.example.com --activation-code <code>
systemctl --user enable --now homestack-agent.service
```

Node 后端监听 `127.0.0.1:19444`，Tailscale Serve 使用 `19443`。如果该端口已被其他 Serve 映射或 Funnel 占用，Node 会直接失败，不覆盖现有配置。

手机必须登录同一 Tailnet。公网 Control 只签发访问票据并跳转到设备 MagicDNS，不代理设备内容。

## 维护

更换 VPS 域名前，先让新旧域名同时反代 `127.0.0.1:18443`。Owner 在 Control 使用当前登录源重新认证并确认新域名；Control 校验新域名 DNS、TLS 和健康接口后更新在线 Node 配置、使现有会话失效并切换域名，不需要重装。

Google 与 GitHub 的切换同样要求当前身份重新认证。新 OAuth 应用必须同时配置页面显示的 `/auth/provider-switch/callback/<provider>` 和正式 `/auth/callback/<provider>` 回调；新身份验证成功后，Control 保持 Owner ID 不变，原子替换登录源配置和 Owner 身份，然后要求重新登录。
