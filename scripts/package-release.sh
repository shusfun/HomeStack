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
  echo "Control 与 Agent 只发布 Linux CLI" >&2
  exit 2
fi
if [[ "$component" =~ ^(agent|desktop)$ && -z "${HOMESTACK_UPDATE_PUBLIC_KEY:-}" ]]; then
  echo "Agent 与桌面发布必须通过 HOMESTACK_UPDATE_PUBLIC_KEY 内置 Ed25519 公钥" >&2
  exit 2
fi

repo_root=$(git rev-parse --show-toplevel)
cd "$repo_root"
brand_dir="$repo_root/assets/brand"
version="${release_tag#v}"
host_os=$(go env GOHOSTOS)
host_arch=$(go env GOHOSTARCH)
if [[ "$target_os" != "$host_os" || "$target_arch" != "$host_arch" ]]; then
  echo "发布打包必须使用目标平台原生 runner，当前为 ${host_os}/${host_arch}，目标为 ${target_os}/${target_arch}" >&2
  exit 2
fi
if [[ "$version" =~ ^([0-9]+\.[0-9]+\.[0-9]+) ]]; then
  package_version="${BASH_REMATCH[1]}"
else
  echo "无法从发布标签解析安装包版本: $release_tag" >&2
  exit 2
fi
commit="${GITHUB_SHA:-$(git rev-parse HEAD)}"
build_date=$(date -u +%Y-%m-%dT%H:%M:%SZ)
output_dir=$(mkdir -p "$output_dir" && cd "$output_dir" && pwd)
stage=$(mktemp -d)
repo_temp=""
cleanup() {
  [[ -z "$repo_temp" ]] || rm -rf -- "$repo_temp"
  rm -rf -- "$stage"
}
trap cleanup EXIT

require_brand_assets() {
  local filename
  for filename in homestack.svg homestack.png homestack.ico homestack.icns; do
    [[ -s "$brand_dir/$filename" ]] || { echo "缺少品牌资源: assets/brand/$filename" >&2; exit 1; }
  done
}

ldflags="-s -w"
ldflags+=" -X github.com/wangshangbin/homestack/internal/buildinfo.Version=$version"
ldflags+=" -X github.com/wangshangbin/homestack/internal/buildinfo.Commit=$commit"
ldflags+=" -X github.com/wangshangbin/homestack/internal/buildinfo.Date=$build_date"
if [[ -n "${HOMESTACK_UPDATE_PUBLIC_KEY:-}" ]]; then
  ldflags+=" -X github.com/wangshangbin/homestack/internal/buildinfo.UpdatePublicKey=$HOMESTACK_UPDATE_PUBLIC_KEY"
fi

export GOENV="${GOENV:-./go.env}"
export GOOS="$target_os"
export GOARCH="$target_arch"

verify_version() {
  local binary="$1" expected_name="$2"
  local metadata
  metadata=$("$binary" --version-json)
  METADATA="$metadata" EXPECTED_NAME="$expected_name" EXPECTED_VERSION="$version" EXPECTED_OS="$target_os" EXPECTED_ARCH="$target_arch" \
    go run ./scripts/verify-version-json.go
}

package_cli() {
  export CGO_ENABLED=0
  local binary="$stage/homestack-$component"
  go build -trimpath -buildvcs=false -ldflags "$ldflags" -o "$binary" "./cmd/homestack-$component"
  verify_version "$binary" "homestack-$component"
  local install_root="$stage/install"
  mkdir -p "$install_root"
  cp "$binary" "$install_root/"
  if [[ "$component" == "agent" ]]; then
    go build -trimpath -buildvcs=false -ldflags "$ldflags" -o "$install_root/homestack-helper" ./cmd/homestack-helper
  fi
  if [[ "$component" == "control" ]]; then
    go build -trimpath -buildvcs=false -ldflags "$ldflags" -o "$install_root/homestack-config-helper" ./cmd/homestack-config-helper
  fi
  cp README.md "$install_root/"
  cp -R docs deploy "$install_root/"
  tar -C "$install_root" -czf "$output_dir/homestack-${component}_${version}_linux_${target_arch}.tar.gz" .
  if [[ "$component" == "agent" ]]; then
    local update_root="$stage/update"
    mkdir -p "$update_root"
    cp "$binary" "$update_root/homestack-agent"
    tar -C "$update_root" -czf "$output_dir/homestack-agent-update_${version}_linux_${target_arch}.tar.gz" homestack-agent
  fi
}

build_desktop() {
  local binary_name="HomeStack"
  local package_path="${desktop_package:-./cmd/homestack-desktop}"
  if [[ "$target_os" == "windows" ]]; then
    binary_name="HomeStack.exe"
    export CGO_ENABLED=0
    ldflags+=" -H windowsgui"
  else
    export CGO_ENABLED=1
  fi
  if [[ "$target_os" == "darwin" ]]; then
    export MACOSX_DEPLOYMENT_TARGET=12.0
    export CGO_CFLAGS="${CGO_CFLAGS:-} -mmacosx-version-min=12.0"
    export CGO_LDFLAGS="${CGO_LDFLAGS:-} -mmacosx-version-min=12.0"
  fi
  if [[ -n "${desktop_overlay:-}" ]]; then
    go build -overlay "$desktop_overlay" -tags production -trimpath -buildvcs=false -ldflags "$ldflags" -o "$stage/$binary_name" "$package_path"
  else
    go build -tags production -trimpath -buildvcs=false -ldflags "$ldflags" -o "$stage/$binary_name" "$package_path"
  fi
  verify_version "$stage/$binary_name" homestack-desktop
}

package_darwin() {
  build_desktop
  local app="$stage/HomeStack.app" contents="$stage/HomeStack.app/Contents"
  mkdir -p "$contents/MacOS" "$contents/Resources"
  mv "$stage/HomeStack" "$contents/MacOS/HomeStack"
  cp "$brand_dir/homestack.icns" "$contents/Resources/HomeStack.icns"
  cp build/darwin/Info.plist "$contents/Info.plist"
  plutil -replace CFBundleExecutable -string HomeStack "$contents/Info.plist"
  plutil -replace CFBundleShortVersionString -string "$package_version" "$contents/Info.plist"
  plutil -replace CFBundleVersion -string "${GITHUB_RUN_NUMBER:-1}" "$contents/Info.plist"
  codesign --force --deep --sign - "$app"
  codesign --verify --deep --strict "$app"
  tar -C "$stage" -czf "$output_dir/HomeStack_${version}_darwin_${target_arch}_update.tar.gz" HomeStack.app
  local dmg_root="$stage/dmg"
  mkdir -p "$dmg_root"
  cp -R "$app" "$dmg_root/HomeStack.app"
  ln -s /Applications "$dmg_root/Applications"
  hdiutil create -quiet -volname HomeStack -srcfolder "$dmg_root" -ov -format UDZO "$output_dir/HomeStack_${version}_darwin_${target_arch}.dmg"
}

package_windows() {
  command -v makensis >/dev/null 2>&1 || { echo "Windows NSIS 打包缺少 makensis" >&2; exit 1; }
  command -v powershell.exe >/dev/null 2>&1 || { echo "Windows 打包缺少 powershell.exe" >&2; exit 1; }
  command -v cygpath >/dev/null 2>&1 || { echo "Windows Git Bash 缺少 cygpath" >&2; exit 1; }
  local portable="$stage/portable" update="$stage/update" assets="$stage/assets"
  mkdir -p "$portable" "$update" "$assets" "$stage/bin"
  go tool wails3 generate build-assets -silent -dir "$assets" -name HomeStack -binaryname HomeStack \
    -productname HomeStack -productcompany HomeStack -productidentifier dev.homestack.desktop \
    -productdescription "HomeStack device connector" -productversion "$package_version" -productcopyright "Copyright 2026 HomeStack"
  cp "$brand_dir/homestack.ico" "$assets/windows/icon.ico"
  # Go 链接器不会从 overlay 读取虚拟 .syso，因此让真实 .syso 只存在于短期包目录。
  repo_temp=$(mktemp -d "$repo_root/build/homestack-windows-package.XXXXXX")
  touch "$repo_temp/main.go"
  local syso="$repo_temp/wails_windows_${target_arch}.syso"
  go tool wails3 generate syso -arch "$target_arch" -icon "$brand_dir/homestack.ico" \
    -manifest "$assets/windows/wails.exe.manifest" -info "$assets/windows/info.json" -out "$syso"
  desktop_overlay="$stage/windows-overlay.json"
  node -e 'const fs=require("node:fs"); fs.writeFileSync(process.argv[1], JSON.stringify({Replace:{[process.argv[2]]:process.argv[3]}}));' \
    "$desktop_overlay" "$repo_temp/main.go" "$repo_root/cmd/homestack-desktop/main.go"
  desktop_package="./${repo_temp#"$repo_root/"}"
  build_desktop
  cp "$stage/HomeStack.exe" "$portable/HomeStack.exe"
  cp README.md "$portable/README.md"
  cp "$stage/HomeStack.exe" "$update/HomeStack.exe"
  local portable_win update_win output_win binary_win
  portable_win=$(cygpath -w "$portable")
  update_win=$(cygpath -w "$update")
  output_win=$(cygpath -w "$output_dir")
  binary_win=$(cygpath -w "$stage/HomeStack.exe")
  powershell.exe -NoProfile -Command "Compress-Archive -LiteralPath '$portable_win\\HomeStack.exe','$portable_win\\README.md' -DestinationPath '$output_win\\HomeStack_${version}_windows_${target_arch}_portable.zip' -CompressionLevel Optimal"
  powershell.exe -NoProfile -Command "Compress-Archive -LiteralPath '$update_win\\HomeStack.exe' -DestinationPath '$output_win\\HomeStack_${version}_windows_${target_arch}_update.zip' -CompressionLevel Optimal"
  local nsis_dir="$assets/windows/nsis" arch_flag=AMD64
  [[ "$target_arch" == "arm64" ]] && arch_flag=ARM64
  go tool wails3 generate webview2bootstrapper -dir "$nsis_dir"
  local nsis_project_win
  nsis_project_win=$(cygpath -w "$nsis_dir/project.nsi")
  makensis -DWAILS_INSTALL_SCOPE=user -DREQUEST_EXECUTION_LEVEL=user \
    "-DARG_WAILS_${arch_flag}_BINARY=$binary_win" "$nsis_project_win"
  local installer
  installer=$(find "$stage/bin" -maxdepth 1 -type f -iname '*installer*.exe' -print -quit)
  [[ -n "$installer" ]] || { echo "Wails NSIS 未生成安装器" >&2; exit 1; }
  cp "$installer" "$output_dir/HomeStack_${version}_windows_${target_arch}_setup.exe"
}

package_linux_desktop() {
  build_desktop
  local appimage_build="$stage/appimage" debroot="$stage/deb"
  mkdir -p "$appimage_build" "$debroot/DEBIAN" "$debroot/usr/bin" "$debroot/usr/share/applications" \
    "$debroot/usr/share/icons/hicolor/512x512/apps"
  go tool wails3 generate .desktop -name HomeStack -exec HomeStack -icon homestack -outputfile "$appimage_build/HomeStack.desktop" -categories "Utility;Network;"
  go tool wails3 generate appimage -binary "$stage/HomeStack" -icon "$brand_dir/homestack.png" \
    -desktopfile "$appimage_build/HomeStack.desktop" -outputdir "$output_dir" -builddir "$appimage_build/build"
  local generated_appimage
  generated_appimage=$(find "$output_dir" -maxdepth 1 -type f -iname '*.AppImage' -print -quit)
  [[ -n "$generated_appimage" ]] || { echo "Wails 未生成 AppImage" >&2; exit 1; }
  mv "$generated_appimage" "$output_dir/HomeStack_${version}_linux_${target_arch}.AppImage"
  install -m 0755 "$stage/HomeStack" "$debroot/usr/bin/HomeStack"
  install -m 0644 "$appimage_build/HomeStack.desktop" "$debroot/usr/share/applications/dev.homestack.desktop.desktop"
  install -m 0644 "$brand_dir/homestack.png" "$debroot/usr/share/icons/hicolor/512x512/apps/homestack.png"
  cat > "$debroot/DEBIAN/control" <<EOF
Package: homestack
Version: $version
Section: utils
Priority: optional
Architecture: $([[ "$target_arch" == amd64 ]] && echo amd64 || echo arm64)
Maintainer: HomeStack
Depends: libgtk-4-1, libwebkitgtk-6.0-4
Description: HomeStack device connector
EOF
  dpkg-deb --root-owner-group --build "$debroot" "$output_dir/HomeStack_${version}_linux_${target_arch}.deb"
}

case "$component" in
  control|agent) package_cli ;;
  desktop)
    require_brand_assets
    case "$target_os" in
      darwin) package_darwin ;;
      windows) package_windows ;;
      linux) package_linux_desktop ;;
    esac
    ;;
esac

find "$output_dir" -maxdepth 1 -type f -print
