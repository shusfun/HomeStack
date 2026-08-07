# Release 与更新

## 发布资产

GitHub Actions 只发布已存在且符合 `vX.Y.Z` 的标签。测试通过后，各平台在原生 runner 生成：

- macOS amd64/arm64：推荐安装的 `.dmg`，以及只含单顶层 `HomeStack.app` 的 `_update.tar.gz`。
- Windows amd64/arm64：当前用户范围 NSIS `_setup.exe`、`_portable.zip`，以及只含单顶层 `HomeStack.exe` 的 `_update.zip`。
- Linux amd64/arm64：AppImage 和 `.deb`。
- Linux amd64/arm64：Control、Agent 安装 `.tar.gz`，以及 Agent 单文件更新 `.tar.gz`。
- 参考 CC Switch，只为自动安装或更新实际消费的 12 个载荷发布 `.sig`，并发布内嵌更新签名的 Wails schema `latest.json`；连同 20 个基础载荷共 33 个 Release 资产。

`latest.json` 有 6 个 desktop 更新项和 2 个架构各自对应的 Agent 更新项，共 8 项。Wails 对同平台/架构选择首个资产，因此生成器显式保证 desktop 项排在 Agent 项之前。

## 签名门禁

- `HOMESTACK_UPDATE_PRIVATE_KEY` 只存 GitHub Actions Secret；`HOMESTACK_UPDATE_PUBLIC_KEY` 注入客户端和 Release 安装命令。
- 每个更新资产同时记录文件名、大小、平台、架构、SHA-256 和 `Ed25519(Sign(SHA-256(asset)))`。
- 发布生成器要求完整 20 个基础资产；未知文件、缺失资产、公私钥不匹配都会阻断 Release。
- 桌面与 Agent 下载后运行暂存程序 `--version-json`，再核对版本、GOOS 和 GOARCH。
- AppImage 使用 Wails 原位更新；deb 只显示 GitHub 下载入口，不绕过包管理器提权。

当前 macOS 只做 ad-hoc codesign、不公证；Windows 不做 Authenticode。Release 必须明确 Gatekeeper/SmartScreen 提示。未来取得商业凭据后再将 Developer ID、公证和 Authenticode 设为稳定版门禁。

## 创建发布

```bash
git tag -a v0.1.3 -m "HomeStack v0.1.3"
git push origin v0.1.3
```

工作流不创建标签、不覆盖 Release，也不发布部分矩阵产物。六个平台桌面包和两个 Linux CLI 架构必须全部成功。

## 本地打包

```bash
export HOMESTACK_UPDATE_PUBLIC_KEY='REPLACE_WITH_BASE64_ED25519_PUBLIC_KEY'
GOENV=./go.env go tool wails3 task release:package \
  COMPONENT=desktop VERSION=v0.1.3 GOOS=darwin ARCH=arm64
```

Control 与 Agent 只允许 Linux；桌面必须在目标操作系统和目标架构的原生环境构建。发布前运行：

```bash
bash -n deploy/install.sh
bash -n scripts/package-release.sh
node ~/.codex/skills/guard-repo-foundations/scripts/audit-repo-foundations.mjs --strict .
```

## 安装器边界

安装器要求 `--update-public-key` 或 `HOMESTACK_UPDATE_PUBLIC_KEY`，同时验证资产 `.sig` 和 SHA-256。已有配置不会覆盖；升级运行中服务会先停止，替换成功后再启动。

安装器不会自动申请 DNS/TLS、安装第三方组件、降低 HTTPS、启用公共 DERP/出口节点，也不会在加密凭据不可用时写明文密钥。
