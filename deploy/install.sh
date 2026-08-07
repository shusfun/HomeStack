#!/usr/bin/env bash

set -Eeuo pipefail

readonly GITHUB_REPO="${HOMESTACK_GITHUB_REPO:-shusfun/HomeStack}"
readonly INSTALL_ROOT="/usr/local/share/homestack"

usage() {
  cat <<'EOF'
HomeStack Linux 安装器

用法:
  install.sh [install|upgrade] <control|agent> [--version vX.Y.Z] [--reset-setup-token] --update-public-key <base64>

示例:
  install.sh control
  install.sh agent
  install.sh upgrade control
  install.sh agent --version v0.2.1

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
reset_setup_token="false"

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
    --reset-setup-token)
      reset_setup_token="true"
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
if [[ "$reset_setup_token" == "true" && "$component" != "control" ]]; then
  fail "--reset-setup-token 只允许用于 Control"
fi

[[ "$(uname -s)" == "Linux" ]] || fail "一键安装脚本只支持 Linux"

case "$(uname -m)" in
  x86_64|amd64) arch="amd64" ;;
  aarch64|arm64) arch="arm64" ;;
  *) fail "不支持的处理器架构: $(uname -m)" ;;
esac

for dependency in curl tar grep install mktemp sed openssl base64 wc cp mkdir rm chmod chown id getent cat; do
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
  if [[ "$reset_setup_token" == "true" && -e /var/lib/homestack-setup/completed.json ]]; then
    fail "Setup 已完成，禁止重置一次性令牌"
  fi
  if [[ -e /etc/homestack/control-signing.key && ! -e /etc/homestack/control-signing.pub ]] || [[ ! -e /etc/homestack/control-signing.key && -e /etc/homestack/control-signing.pub ]]; then
    fail "Control Ed25519 签名密钥对不完整，拒绝覆盖或重新生成"
  fi
  command -v systemctl >/dev/null 2>&1 || fail "系统缺少 systemctl"
  command -v useradd >/dev/null 2>&1 || fail "系统缺少 useradd"
  [[ -f "$work_dir/archive/homestack-config-helper" ]] || fail "Control Release 缺少 homestack-config-helper"
  for unit in homestack-control.service homestack-setup.service homestack-config-helper.service homestack-setup-switch.service; do
    [[ -f "$work_dir/archive/deploy/systemd/$unit" ]] || fail "Control Release 缺少 systemd unit: $unit"
  done
  for template in deploy/env/control.env.example; do
    [[ -f "$work_dir/archive/$template" ]] || fail "Control Release 缺少 Setup 模板: $template"
  done

  local was_active="false"
  if systemctl is-active --quiet homestack-control.service; then
    was_active="true"
    systemctl stop homestack-control.service
  fi

  if ! id -u homestack-control >/dev/null 2>&1; then
    useradd --system --user-group --home-dir /var/lib/homestack-control --shell /usr/sbin/nologin homestack-control
  fi
  getent group homestack-control >/dev/null 2>&1 || fail "系统用户 homestack-control 缺少同名系统组"

  install -d -m 0755 "$INSTALL_ROOT"
  install -d -m 0755 "$INSTALL_ROOT/deploy" /usr/local/libexec
  install -d -m 0750 -o homestack-control -g homestack-control /etc/homestack
  install -d -m 0700 -o root -g root /var/lib/homestack-setup
  install -d -m 0750 -o homestack-control -g homestack-control /var/lib/homestack-control
  if [[ -x /usr/local/bin/homestack-control ]]; then
    cp -p /usr/local/bin/homestack-control "$work_dir/previous-homestack-control"
  fi
  if [[ -x /usr/local/libexec/homestack-config-helper ]]; then
    cp -p /usr/local/libexec/homestack-config-helper "$work_dir/previous-homestack-config-helper"
  fi
  install -m 0755 "$binary" /usr/local/bin/homestack-control
  install -m 0755 "$work_dir/archive/homestack-config-helper" /usr/local/libexec/homestack-config-helper
  cp -R "$work_dir/archive/deploy/." "$INSTALL_ROOT/deploy/"
  install -m 0644 "$work_dir/archive/deploy/systemd/homestack-control.service" /etc/systemd/system/homestack-control.service
  install -m 0644 "$work_dir/archive/deploy/systemd/homestack-setup.service" /etc/systemd/system/homestack-setup.service
  install -m 0644 "$work_dir/archive/deploy/systemd/homestack-setup-switch.service" /etc/systemd/system/homestack-setup-switch.service
  sed "s/REPLACE_WITH_CONTROL_UID/$(id -u homestack-control)/g" "$work_dir/archive/deploy/systemd/homestack-config-helper.service" > "$work_dir/homestack-config-helper.service"
  install -m 0644 "$work_dir/homestack-config-helper.service" /etc/systemd/system/homestack-config-helper.service
  printf '%s\n' "$version" > "$INSTALL_ROOT/control-version"

  local setup_needed="false"
  if [[ ! -e /etc/homestack/control.env ]]; then
    install -m 0600 "$work_dir/archive/deploy/env/control.env.example" /etc/homestack/control.env
  fi
  /usr/local/libexec/homestack-config-helper migrate-config
  if grep -Eq 'REPLACE_WITH_|example\.com' /etc/homestack/control.env; then
    setup_needed="true"
    if [[ ! -e "$INSTALL_ROOT/control.env.pre-setup" ]]; then
      install -m 0600 /etc/homestack/control.env "$INSTALL_ROOT/control.env.pre-setup"
    fi
  fi
  if [[ -e /var/lib/homestack-setup/state.json && ! -e /var/lib/homestack-setup/completed.json ]]; then
    setup_needed="true"
  fi
  chown homestack-control:homestack-control /etc/homestack/control.env
  chmod 0600 /etc/homestack/control.env
  if [[ ! -e /etc/homestack/control-signing.key && ! -e /etc/homestack/control-signing.pub ]]; then
    /usr/local/bin/homestack-control keygen \
      --private /etc/homestack/control-signing.key \
      --public /etc/homestack/control-signing.pub
  fi
  chown homestack-control:homestack-control /etc/homestack/control-signing.key /etc/homestack/control-signing.pub
  chmod 0600 /etc/homestack/control-signing.key
  chmod 0644 /etc/homestack/control-signing.pub
  if [[ "$setup_needed" == "false" ]]; then
    if ! /usr/local/bin/homestack-control configtest --env-file /etc/homestack/control.env; then
      if [[ -f /etc/homestack/control.env.pre-0.2.1 ]]; then
        install -m 0600 -o homestack-control -g homestack-control /etc/homestack/control.env.pre-0.2.1 /etc/homestack/control.env
      fi
      if [[ -f "$work_dir/previous-homestack-control" ]]; then
        install -m 0755 "$work_dir/previous-homestack-control" /usr/local/bin/homestack-control
      fi
      if [[ -f "$work_dir/previous-homestack-config-helper" ]]; then
        install -m 0755 "$work_dir/previous-homestack-config-helper" /usr/local/libexec/homestack-config-helper
      fi
      fail "迁移后的 Control 配置校验失败，已恢复升级前配置和二进制"
    fi
  fi

  systemctl daemon-reload
  systemctl enable homestack-config-helper.service
  systemctl restart homestack-config-helper.service
  if [[ "$setup_needed" == "true" && ! -e /var/lib/homestack-setup/completed.json ]]; then
    if [[ "$reset_setup_token" == "true" ]]; then
      rm -f -- /etc/homestack/setup-token.sha256 /etc/homestack/setup-session.json
    fi
    local setup_token="" setup_hash
    if [[ ! -e /etc/homestack/setup-token.sha256 && ! -e /etc/homestack/setup-session.json ]]; then
      setup_token=$(openssl rand -hex 32) || fail "生成 Setup 令牌失败"
      setup_hash=$(printf '%s' "$setup_token" | openssl dgst -sha256 | sed 's/^.*= //') || fail "计算 Setup 令牌摘要失败"
      printf '%s\n' "$setup_hash" > "$work_dir/setup-token.sha256"
      install -m 0400 -o homestack-control -g homestack-control "$work_dir/setup-token.sha256" /etc/homestack/setup-token.sha256
    fi
    systemctl disable --now homestack-control.service >/dev/null 2>&1 || true
    systemctl enable homestack-setup.service
    systemctl restart homestack-setup.service
    echo "Setup 端口: http://127.0.0.1:18443"
    if [[ -n "$setup_token" ]]; then
      echo "Setup 一次性令牌: $setup_token"
    else
      echo "Setup 令牌或会话已存在，本次安装未重新生成。"
    fi
  elif [[ "$was_active" == "true" ]]; then
    systemctl start homestack-control.service
  fi

  echo "HomeStack Control $release_tag 已安装。"
  if [[ "$setup_needed" == "true" ]]; then
    echo "请将宝塔唯一反向代理指向 http://127.0.0.1:18443，然后打开 /setup 完成初始化。"
  else
    echo "现有 Control 配置已保留。"
  fi
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
  echo "请执行：homestack-agent activate --server https://你的域名 --activation-code 激活码，然后启用 homestack-agent 用户服务。"
}

case "$component" in
  control) install_control ;;
  agent) install_agent ;;
esac

if [[ "$command_name" == "upgrade" ]]; then
  echo "升级完成；现有配置未被覆盖。"
fi
