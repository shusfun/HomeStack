#!/usr/bin/env bash

set -Eeuo pipefail

readonly GITHUB_REPO="${HOMESTACK_GITHUB_REPO:-shusfun/HomeStack}"
readonly INSTALL_ROOT="/usr/local/share/homestack"

usage() {
  cat <<'EOF'
HomeStack Linux 安装器

用法:
  install.sh [install|upgrade] <control|agent> [--version vX.Y.Z] --update-public-key <base64>

示例:
  install.sh control
  install.sh agent
  install.sh upgrade control
  install.sh agent --version v0.1.3

Control 需要 root；Agent 必须以最终使用者身份运行，不能使用 sudo。
EOF
}

fail() {
  echo "错误: $*" >&2
  exit 1
}

command_name="install"
component=""
requested_version=""
update_public_key="${HOMESTACK_UPDATE_PUBLIC_KEY:-}"

while [[ $# -gt 0 ]]; do
  case "$1" in
    install|upgrade)
      command_name="$1"
      shift
      ;;
    control|agent)
      [[ -z "$component" ]] || fail "只能指定一个安装组件"
      component="$1"
      shift
      ;;
    -v|--version)
      [[ -n "${2:-}" ]] || fail "$1 必须提供版本号"
      requested_version="$2"
      shift 2
      ;;
    --version=*)
      requested_version="${1#*=}"
      shift
      ;;
    --update-public-key)
      [[ -n "${2:-}" ]] || fail "$1 必须提供 base64 Ed25519 公钥"
      update_public_key="$2"
      shift 2
      ;;
    --update-public-key=*)
      update_public_key="${1#*=}"
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      fail "未知参数: $1"
      ;;
  esac
done

[[ -n "$component" ]] || {
  usage
  exit 2
}

[[ "$(uname -s)" == "Linux" ]] || fail "一键安装脚本只支持 Linux"

case "$(uname -m)" in
  x86_64|amd64) arch="amd64" ;;
  aarch64|arm64) arch="arm64" ;;
  *) fail "不支持的处理器架构: $(uname -m)" ;;
esac

for dependency in curl tar sha256sum awk grep install mktemp sed openssl base64 wc; do
  command -v "$dependency" >/dev/null 2>&1 || fail "缺少命令: $dependency"
done
[[ -n "$update_public_key" ]] || fail "必须通过 --update-public-key 或 HOMESTACK_UPDATE_PUBLIC_KEY 提供 Ed25519 公钥"

normalize_version() {
  local value="$1"
  [[ "$value" == v* ]] || value="v$value"
  [[ "$value" =~ ^v[0-9]+\.[0-9]+\.[0-9]+([.-][A-Za-z0-9.-]+)?$ ]] || fail "版本号格式无效: $value"
  echo "$value"
}

latest_version() {
  local release_url
  release_url=$(curl -fsSL -o /dev/null -w '%{url_effective}' "https://github.com/$GITHUB_REPO/releases/latest") ||
    fail "读取 GitHub 最新 Release 失败"
  [[ "$release_url" == "https://github.com/$GITHUB_REPO/releases/tag/"* ]] ||
    fail "GitHub 最新 Release 跳转地址无效: $release_url"
  normalize_version "${release_url##*/}"
}

if [[ -n "$requested_version" ]]; then
  release_tag=$(normalize_version "$requested_version")
else
  release_tag=$(latest_version)
fi
version="${release_tag#v}"
asset="homestack-${component}_${version}_linux_${arch}.tar.gz"
release_base="https://github.com/$GITHUB_REPO/releases/download/$release_tag"

work_dir=$(mktemp -d)
trap 'rm -rf -- "$work_dir"' EXIT

echo "下载 HomeStack $component $release_tag ($arch)"
curl -fL --retry 3 --connect-timeout 10 -o "$work_dir/$asset" "$release_base/$asset"
curl -fL --retry 3 --connect-timeout 10 -o "$work_dir/$asset.sig" "$release_base/$asset.sig"
curl -fL --retry 3 --connect-timeout 10 -o "$work_dir/checksums.txt" "$release_base/checksums.txt"

expected_checksum=$(awk -v name="$asset" '$2 == name || $2 == "*" name { print $1 }' "$work_dir/checksums.txt")
[[ "$expected_checksum" =~ ^[a-fA-F0-9]{64}$ ]] || fail "checksums.txt 中缺少 $asset 的 SHA-256"
printf '%s  %s\n' "$expected_checksum" "$work_dir/$asset" | sha256sum -c - >/dev/null || fail "Release 文件 SHA-256 校验失败"
printf '\x30\x2a\x30\x05\x06\x03\x2b\x65\x70\x03\x21\x00' > "$work_dir/update-public.der"
printf '%s' "$update_public_key" | base64 -d >> "$work_dir/update-public.der" 2>/dev/null || fail "Ed25519 公钥不是有效 base64"
[[ "$(wc -c < "$work_dir/update-public.der")" -eq 44 ]] || fail "Ed25519 公钥必须为 32 字节"
openssl pkey -pubin -inform DER -in "$work_dir/update-public.der" -out "$work_dir/update-public.pem" >/dev/null 2>&1 || fail "Ed25519 公钥无法转换为 OpenSSL 格式"
base64 -d < "$work_dir/$asset.sig" > "$work_dir/$asset.sig.bin" 2>/dev/null || fail "Release Ed25519 签名编码无效"
openssl dgst -sha256 -binary "$work_dir/$asset" > "$work_dir/$asset.sha256.bin" || fail "计算 Release SHA-256 失败"
openssl pkeyutl -verify -pubin -inkey "$work_dir/update-public.pem" -rawin -in "$work_dir/$asset.sha256.bin" -sigfile "$work_dir/$asset.sig.bin" >/dev/null 2>&1 || fail "Release Ed25519 签名校验失败"

mkdir "$work_dir/archive"
tar -xzf "$work_dir/$asset" -C "$work_dir/archive"
binary="$work_dir/archive/homestack-$component"
[[ -f "$binary" ]] || fail "Release 归档缺少 homestack-$component"
chmod 0755 "$binary"
"$binary" --version | grep -F "homestack-$component $version " >/dev/null || fail "Release 二进制版本与标签不一致"

install_control() {
  [[ "$EUID" -eq 0 ]] || fail "安装 Control 必须使用 root，例如 curl ... | sudo bash -s -- control"
	command -v systemctl >/dev/null 2>&1 || fail "系统缺少 systemctl"
	command -v useradd >/dev/null 2>&1 || fail "系统缺少 useradd"

  local was_active="false"
  if systemctl is-active --quiet homestack-control.service; then
    was_active="true"
    systemctl stop homestack-control.service
  fi

  if ! id -u homestack-control >/dev/null 2>&1; then
    useradd --system --home-dir /var/lib/homestack-control --shell /usr/sbin/nologin homestack-control
  fi

  install -d -m 0755 "$INSTALL_ROOT"
  install -d -m 0750 -o homestack-control -g homestack-control /etc/homestack
  install -m 0755 "$binary" /usr/local/bin/homestack-control
  install -m 0644 "$work_dir/archive/deploy/systemd/homestack-control.service" /etc/systemd/system/homestack-control.service
  printf '%s\n' "$version" > "$INSTALL_ROOT/control-version"

  if [[ ! -e /etc/homestack/control.env ]]; then
    install -m 0600 "$work_dir/archive/deploy/env/control.env.example" /etc/homestack/control.env
  fi
  if [[ ! -e /etc/homestack/signing-private.key && ! -e /etc/homestack/signing-public.key ]]; then
    /usr/local/bin/homestack-control keygen \
      --private /etc/homestack/signing-private.key \
      --public /etc/homestack/signing-public.key
    chown homestack-control:homestack-control /etc/homestack/signing-private.key /etc/homestack/signing-public.key
  fi

  systemctl daemon-reload
  if [[ "$was_active" == "true" ]]; then
    systemctl start homestack-control.service
  fi

  echo "HomeStack Control $release_tag 已安装。"
  echo "请完成 /etc/homestack/control.env、HTTPS 证书、至少一种登录方式与 Headscale 配置后再启用服务。"
}

install_agent() {
  [[ "$EUID" -ne 0 ]] || fail "Agent 必须以最终使用者身份运行，不能使用 root 或 sudo"
  command -v systemctl >/dev/null 2>&1 || fail "系统缺少 systemctl"
  command -v sudo >/dev/null 2>&1 || fail "Agent 安装 root helper 需要 sudo"
  command -v loginctl >/dev/null 2>&1 || fail "系统缺少 loginctl"
  command -v tailscale >/dev/null 2>&1 || fail "系统缺少官方 tailscale 客户端"

  local config_dir="$HOME/.config/homestack"
  local state_dir="$HOME/.local/state/homestack"
  local service_dir="$HOME/.config/systemd/user"
  local was_active="false"
  if systemctl --user is-active --quiet homestack-agent.service; then
    was_active="true"
    systemctl --user stop homestack-agent.service
  fi

  install -d -m 0700 "$config_dir" "$state_dir"
  install -d -m 0755 "$HOME/.local/bin" "$service_dir"
  install -m 0755 "$binary" "$HOME/.local/bin/homestack-agent"
  install -m 0644 "$work_dir/archive/deploy/systemd/user/homestack-agent.service" "$service_dir/homestack-agent.service"
  printf '%s\n' "$version" > "$config_dir/agent-version"

  [[ -f "$work_dir/archive/homestack-helper" ]] || fail "Agent Release 缺少 homestack-helper"
  sed "s/REPLACE_WITH_AGENT_UID/$(id -u)/g" "$work_dir/archive/deploy/systemd/homestack-helper.service" > "$work_dir/homestack-helper.service"
  sudo install -d -m 0755 /usr/local/libexec
  sudo install -m 0755 "$work_dir/archive/homestack-helper" /usr/local/libexec/homestack-helper
  sudo install -m 0644 "$work_dir/homestack-helper.service" /etc/systemd/system/homestack-helper.service
  sudo systemctl daemon-reload
  sudo systemctl enable homestack-helper.service
  sudo systemctl restart homestack-helper.service
  sudo loginctl enable-linger "$USER"
  sudo tailscale set --operator="$USER"

  if [[ ! -e "$config_dir/agent.env" ]]; then
    sed "s|/home/REPLACE_WITH_USER|$HOME|g" "$work_dir/archive/deploy/env/agent.env.example" > "$work_dir/agent.env"
    install -m 0600 "$work_dir/agent.env" "$config_dir/agent.env"
  fi

  systemctl --user daemon-reload
  if [[ "$was_active" == "true" ]]; then
    systemctl --user start homestack-agent.service
  fi

  echo "HomeStack Agent $release_tag 已安装。"
  echo "请执行桌面端生成的 enroll 命令，并用 systemd-creds encrypt --uid=self 分别创建 tls.crt 与 tls.key 后再启用用户服务。"
}

case "$component" in
  control) install_control ;;
  agent) install_agent ;;
esac

if [[ "$command_name" == "upgrade" ]]; then
  echo "升级完成；现有配置未被覆盖。"
fi
