#!/usr/bin/env bash

set -Eeuo pipefail

usage() {
  echo "用法: $0 <control|agent|desktop> <vX.Y.Z> <linux|darwin|windows> <amd64|arm64> [输出目录]" >&2
}

component="${1:-}"
release_tag="${2:-}"
target_os="${3:-}"
target_arch="${4:-}"
output_dir="${5:-dist}"

if [[ ! "$component" =~ ^(control|agent|desktop)$ ]] ||
  [[ ! "$release_tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+([.-][A-Za-z0-9.-]+)?$ ]] ||
  [[ ! "$target_os" =~ ^(linux|darwin|windows)$ ]] ||
  [[ ! "$target_arch" =~ ^(amd64|arm64)$ ]]; then
  usage
  exit 2
fi

if [[ "$component" != "desktop" && "$target_os" != "linux" ]]; then
  echo "Control 与 Agent 的一键安装产物只发布 Linux 版本" >&2
  exit 2
fi

repo_root=$(git rev-parse --show-toplevel)
cd "$repo_root"

version="${release_tag#v}"
commit="${GITHUB_SHA:-$(git rev-parse HEAD)}"
build_date=$(date -u +%Y-%m-%dT%H:%M:%SZ)

ldflags="-s -w"
ldflags+=" -X github.com/wangshangbin/homestack/internal/buildinfo.Version=$version"
ldflags+=" -X github.com/wangshangbin/homestack/internal/buildinfo.Commit=$commit"
ldflags+=" -X github.com/wangshangbin/homestack/internal/buildinfo.Date=$build_date"

mkdir -p "$output_dir"
stage=$(mktemp -d)
trap 'rm -rf -- "$stage"' EXIT

export GOENV="${GOENV:-./go.env}"
export GOOS="$target_os"
export GOARCH="$target_arch"

archive_base="homestack-${component}_${version}_${target_os}_${target_arch}"

case "$component" in
  control|agent)
    export CGO_ENABLED=0
    go build -trimpath -buildvcs=false -ldflags "$ldflags" -o "$stage/homestack-$component" "./cmd/homestack-$component"
    cp README.md "$stage/"
    cp -R docs deploy "$stage/"
    ;;
  desktop)
    binary_name="homestack-desktop"
    if [[ "$target_os" == "windows" ]]; then
      export CGO_ENABLED=0
      binary_name+=".exe"
      ldflags+=" -H windowsgui"
    else
      export CGO_ENABLED=1
    fi
    if [[ "$target_os" == "darwin" ]]; then
      export MACOSX_DEPLOYMENT_TARGET=12.0
      export CGO_CFLAGS="${CGO_CFLAGS:-} -mmacosx-version-min=12.0"
      export CGO_LDFLAGS="${CGO_LDFLAGS:-} -mmacosx-version-min=12.0"
    fi
    go build -tags production -trimpath -buildvcs=false -ldflags "$ldflags" -o "$stage/$binary_name" ./cmd/homestack-desktop
    if [[ "$target_os" == "darwin" ]]; then
      app_dir="$stage/HomeStack.app/Contents"
      mkdir -p "$app_dir/MacOS" "$app_dir/Resources"
      mv "$stage/$binary_name" "$app_dir/MacOS/homestack-desktop"
      cp build/darwin/Info.plist "$app_dir/Info.plist"
      plutil -replace CFBundleShortVersionString -string "${version%%-*}" "$app_dir/Info.plist"
      plutil -replace CFBundleVersion -string "${GITHUB_RUN_NUMBER:-1}" "$app_dir/Info.plist"
      codesign --force --deep --sign - "$stage/HomeStack.app"
    else
      cp README.md "$stage/"
    fi
    ;;
esac

tar -C "$stage" -czf "$output_dir/$archive_base.tar.gz" .
echo "$output_dir/$archive_base.tar.gz"
