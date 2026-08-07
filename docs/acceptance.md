# 验收

- 新安装只监听 `127.0.0.1:18443`，Setup 能完成单域名和单 OAuth 配置。
- Setup 令牌单次使用、24 小时会话、Origin 校验、限流和完成后永久锁定均通过测试。
- Control `/api/meta` 只返回一个登录方式，Secret 不出现在任何状态响应中。
- 激活码十分钟过期、只能兑换一次，并与 OAuth 登录共用设备登记服务。
- Node 仅监听 `127.0.0.1:19444`，Tailscale Serve 仅添加 `19443` 映射；测试中不得出现 `tailscale up`。
- 首台设备锁定 Tailnet MagicDNS 后缀，后续设备必须一致。
- 手机从 Control 获取 30 秒单次票据后跳转到设备 MagicDNS，VPS 不承载设备业务流量。
- 共享目录只读，路径穿越、符号链接越界和特殊设备文件均被拒绝。
- Go、Vitest、typecheck、前端构建、Shell 语法、Release 清单和严格仓库审计全部通过。
