# 原生部署

本文档只给出显式安装步骤，不下载、不安装、不升级第三方组件。所有示例域名和 `REPLACE_WITH_*` 必须先替换，残留占位符视为部署失败。

## 1. 域名与端口

为 VPS 配置以下 DNS 记录：

| 地址 | 端口 | 服务 |
| --- | --- | --- |
| `mesh.example.com` | TCP 443、UDP 3478 | Headscale、DERP、STUN |
| `app.example.com` | TCP 8443 | HomeStack Control |
| `id.example.com` | TCP 8444 | Pocket ID |

防火墙只开放上表端口。不要开放 Headscale gRPC、metrics、FileBrowser、Jellyfin 或 cc-connect 端口。Headscale 使用内置 ACME TLS-ALPN-01；Control 与 Pocket ID 使用你通过 DNS-01 或其他受控方式签发的证书。

每台设备另准备仅解析到其 Tailscale IP 的名称，例如 `nas.tail.example.com -> 100.64.0.10`，并通过 DNS-01 签发受系统信任的证书。该名称只用于 Agent HTTPS，不应把 Agent 端口暴露到公网。

## 2. Pocket ID

1. 从官方发布页安装 Pocket ID `v2.12.0` 到 `/usr/local/bin/pocket-id`，先核对发布校验值并运行 `pocket-id version`。
2. 创建专用 `pocket-id` 用户，将 [deploy/systemd/pocket-id.service](../deploy/systemd/pocket-id.service) 安装到 `/etc/systemd/system/`。
3. 将 [deploy/env/pocket-id.env.example](../deploy/env/pocket-id.env.example) 安装为权限 `0600` 的 `/etc/pocket-id/pocket-id.env`，证书与私钥仅授权该用户读取。
4. `ENCRYPTION_KEY_FILE` 指向至少 16 字节的独立随机密钥文件。禁止把密钥写进 unit 或提交到仓库。
5. 首次登录后创建 `homestack-users` 和 `homestack-admins` 组。

创建两个 OIDC 客户端：

| 客户端 | 类型 | PKCE | 回调地址 |
| --- | --- | --- | --- |
| HomeStack | Public | S256 | `https://app.example.com:8443/auth/callback`、`http://127.0.0.1/callback` |
| Headscale | Confidential | S256 | `https://mesh.example.com/oidc/callback` |

Pocket ID 的 `ALLOW_INSECURE_CALLBACK_URLS=true` 只为已登记的 RFC 8252 回环地址服务；不要登记其他 HTTP 回调。

## 3. Headscale 与自有 DERP

1. 从官方发布页安装 Headscale `v0.29.3`，核对校验值并运行 `headscale version`。
2. 将 [deploy/headscale/config.yaml](../deploy/headscale/config.yaml) 和 [deploy/headscale/policy.hujson](../deploy/headscale/policy.hujson) 安装到 `/etc/headscale/`。
3. 替换域名、VPS 公网 IPv4、ACME 邮箱、OIDC Client ID，把 Pocket ID 客户端密钥写入权限 `0640` 的 `/etc/headscale/oidc-client-secret`。
4. 安装 [deploy/systemd/headscale.service](../deploy/systemd/headscale.service)，运行 `headscale configtest --config /etc/headscale/config.yaml` 和 `headscale policy check --config /etc/headscale/config.yaml --bypass`，两者必须成功。
5. 确认 `tailscale debug derp-map` 只有 `homestack` 区域，不含公共 DERP。

策略只开放本人设备 TCP `9443`。不要加入 `autogroup:internet`、`*:*`、子网路由或出口节点授权。

## 4. HomeStack Control

可在安装完 Headscale 与 Pocket ID 后使用 Release 一键安装 HomeStack 自身：

```bash
curl -fsSL https://raw.githubusercontent.com/shusfun/HomeStack/main/deploy/install.sh | sudo bash -s -- control
```

安装器会校验 Release SHA-256、安装二进制和 systemd unit、创建 Control 签名密钥，但不会启动残留占位符的服务，也不会安装任何外部组件。

1. 使用一键安装器，或在受控构建机从本仓库构建 `homestack-control` 并安装到 `/usr/local/bin/`。
2. 运行 `homestack-control keygen --private /etc/homestack/signing-private.key --public /etc/homestack/signing-public.key`。私钥权限必须为 `0600` 且离线备份。
3. 将 [deploy/env/control.env.example](../deploy/env/control.env.example) 安装为 `/etc/homestack/control.env` 并替换域名、OIDC Client ID 和管理员组。
4. 安装 [deploy/systemd/homestack-control.service](../deploy/systemd/homestack-control.service)。Control 用户必须通过 `headscale` 组访问 `/run/headscale/headscale.sock`，不得开放远程 gRPC。
5. 启动后只接受 `https://app.example.com:8443`，HTTP 请求不做重定向或降级。

## 5. 设备端

1. 由用户确认后安装官方 Tailscale `v1.102.2`、FileBrowser Quantum `v0.3.5`、Jellyfin `v10.11.11` 和 cc-connect `v1.4.1`。HomeStack 不重新分发这些文件。
2. FileBrowser 使用 [deploy/filebrowser/config.yaml](../deploy/filebrowser/config.yaml) 和 [deploy/systemd/filebrowser.service](../deploy/systemd/filebrowser.service)。共享根目录以只读方式提供，API Token 只授予 `api` 与 `download`。
3. Jellyfin 必须只监听 `127.0.0.1:8096`，不得通过反向代理或公网防火墙暴露。
4. 以最终使用者身份运行 `curl -fsSL https://raw.githubusercontent.com/shusfun/HomeStack/main/deploy/install.sh | bash -s -- agent`，或手工将 `homestack-agent` 安装到 `~/.local/bin/` 并安装 [deploy/systemd/user/homestack-agent.service](../deploy/systemd/user/homestack-agent.service)。不得使用 root 或 `sudo` 安装 Agent。
5. 将 [deploy/env/agent.env.example](../deploy/env/agent.env.example) 安装为权限 `0600` 的 `~/.config/homestack/agent.env`。监听地址必须是该设备的 Tailscale IP 和端口 `9443`，证书名称必须与邀请中的 Agent URL 一致。
6. Linux 设备先确认用户会话的 Secret Service 可用，再运行桌面端加入。安全存储不可用时不要改成明文文件。
7. 管理员在 Control 中填写设备名、Agent HTTPS 地址和模块信息，生成一次性连接信息；设备端只需粘贴并完成 Passkey。

手机首次在官方 Tailscale 客户端中把自定义协调服务器设为 `https://mesh.example.com` 并登录，随后使用浏览器访问 Control。业务访问票据会将浏览器直接带到设备 Agent 地址。

## 6. 启动与检查

```bash
systemctl enable --now pocket-id.service
systemctl enable --now headscale.service
systemctl enable --now homestack-control.service
systemctl enable --now filebrowser.service
systemctl --user enable --now homestack-agent.service
```

逐项检查服务状态和日志，不要把启动失败替换成其他监听地址、公共 DERP 或 HTTP。完整验收见 [acceptance.md](acceptance.md)。
