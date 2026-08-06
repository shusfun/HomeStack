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
5. 创建 HomeStack OIDC 客户端。HomeStack 自身采用单人所有者模型，不依赖 Pocket ID 用户组或管理员组。

创建两个 OIDC 客户端：

| 客户端 | 类型 | PKCE | 回调地址 |
| --- | --- | --- | --- |
| HomeStack | Confidential | S256 | `https://app.example.com:8443/auth/callback/pocket` |
| Headscale | Confidential | S256 | `https://mesh.example.com/oidc/callback` |

App 回环地址由 Control 自己处理，不注册到 Pocket ID。Google 回调为 `/auth/callback/google`，GitHub 回调为 `/auth/callback/github`。

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
UPDATE_PUBLIC_KEY='REPLACE_WITH_BASE64_ED25519_PUBLIC_KEY'
curl -fsSL https://raw.githubusercontent.com/shusfun/HomeStack/main/deploy/install.sh | sudo bash -s -- control --update-public-key "$UPDATE_PUBLIC_KEY"
```

安装器会校验 Release SHA-256 与 Ed25519 签名、安装二进制和 systemd unit、创建 Control 签名密钥，但不会启动残留占位符的服务，也不会安装任何外部组件。

1. 使用一键安装器，或在受控构建机从本仓库构建 `homestack-control` 并安装到 `/usr/local/bin/`。
2. 运行 `homestack-control keygen --private /etc/homestack/signing-private.key --public /etc/homestack/signing-public.key`。私钥权限必须为 `0600` 且离线备份。
3. 将 [deploy/env/control.env.example](../deploy/env/control.env.example) 安装为 `/etc/homestack/control.env`，替换域名并完整配置至少一个 Pocket ID、Google 或 GitHub 登录方式。
4. 安装 [deploy/systemd/homestack-control.service](../deploy/systemd/homestack-control.service)。Control 用户必须通过 `headscale` 组访问 `/run/headscale/headscale.sock`，不得开放远程 gRPC。
5. 启动后只接受 `https://app.example.com:8443`，HTTP 请求不做重定向或降级。首次公网开放后立即登录；第一个成功登录者会成为唯一所有者。

## 5. 设备端

1. 由用户确认后安装官方 Tailscale `v1.102.2`、FileBrowser Quantum `v0.3.5`、Jellyfin `v10.11.11` 和 cc-connect `v1.4.1`。HomeStack 不重新分发这些文件。
2. FileBrowser 使用 [deploy/filebrowser/config.yaml](../deploy/filebrowser/config.yaml) 和 [deploy/systemd/filebrowser.service](../deploy/systemd/filebrowser.service)。共享根目录以只读方式提供，API Token 只授予 `api` 与 `download`。
3. Jellyfin 必须只监听 `127.0.0.1:8096`，不得通过反向代理或公网防火墙暴露。
4. 以最终使用者身份运行 Agent 安装器并传入更新公钥。安装器使用 `sudo` 安装固定 root helper、启用 linger，并将当前用户设为 Tailscale operator；不要对整个脚本使用 sudo。
5. 将 [deploy/env/agent.env.example](../deploy/env/agent.env.example) 安装为权限 `0600` 的 `~/.config/homestack/agent.env`。监听地址必须是设备 Tailscale IP 和 `9443`。
6. 用 DNS-01 为 Agent 域名签发可信证书，分别执行 `systemd-creds encrypt --uid=self --name=tls.crt fullchain.pem ~/.config/credstore.encrypted/tls.crt` 和对应的 `tls.key` 命令。
7. 在 App 或 Control 填写设备名、Agent HTTPS 地址和模块信息，生成十分钟单次配对命令并在 Linux 上执行。命令加入 Tailnet，并把设备档案写入 `~/.config/credstore.encrypted/homestack-agent-profile`。

手机首次在官方 Tailscale 客户端中把自定义协调服务器设为 `https://mesh.example.com` 并登录，随后使用浏览器访问 Control。业务访问票据会将浏览器直接带到设备 Agent 地址。

## 6. 启动与检查

```bash
systemctl enable --now pocket-id.service
systemctl enable --now headscale.service
systemctl enable --now homestack-control.service
systemctl enable --now filebrowser.service
systemctl enable --now homestack-helper.service
systemctl --user enable --now homestack-agent.service
```

逐项检查服务状态和日志，不要把启动失败替换成其他监听地址、公共 DERP 或 HTTP。完整验收见 [acceptance.md](acceptance.md)。
