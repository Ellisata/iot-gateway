#!/usr/bin/env bash
# ============================================
#  iot-gateway 发行版打包脚本
#  前端构建 -> Go 交叉编译 -> 生成各平台压缩包 + SHA256 校验
#
#  用法:
#    ./release.sh            # 版本号自动取最新 git tag（如 v0.1.0）
#    ./release.sh v0.2.0     # 手动指定版本号
#
#  产物（release/ 目录）:
#    iot-gateway-<版本>-windows-amd64.zip
#    iot-gateway-<版本>-linux-amd64.tar.gz
#    iot-gateway-<版本>-linux-arm64.tar.gz
#    SHA256SUMS.txt
# ============================================
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OUT="$ROOT/release"
cd "$ROOT"

# ---------- 版本号：参数优先，否则取最新 tag ----------
VERSION="${1:-$(git describe --tags --abbrev=0 2>/dev/null || echo dev)}"
echo "==> 版本: $VERSION"

# ---------- 优先使用 GNU tar（保留可执行权限，Windows 自带 bsdtar 不保留）----------
TAR=tar
if ! tar --version 2>/dev/null | grep -q GNU; then
  for c in /usr/bin/tar /bin/tar; do
    if [ -x "$c" ] && "$c" --version 2>/dev/null | grep -q GNU; then TAR="$c"; break; fi
  done
fi

# ---------- zip 打包工具：Windows 用自带 bsdtar（-a 按扩展名生成 zip）----------
# 注：不用 PowerShell Compress-Archive——从 Git Bash 启动时中文路径编码会损坏
ZIP_TAR=""
if [ -x /c/Windows/System32/tar.exe ]; then
  ZIP_TAR=/c/Windows/System32/tar.exe
elif command -v bsdtar >/dev/null 2>&1; then
  ZIP_TAR=bsdtar
fi

# ---------- 1/4 前端构建 ----------
echo "==> [1/4] 构建前端 (npm run build) ..."
cd "$ROOT/frontend"
if [ ! -d node_modules ]; then
  echo "  node_modules 不存在，先执行 npm install ..."
  npm install
fi
npm run build

# ---------- 2/4 同步前端产物到 embed 目录 ----------
echo "==> [2/4] 同步前端产物到 backend/web/dist ..."
rm -rf "$ROOT/backend/web/dist"
mkdir -p "$ROOT/backend/web/dist"
cp -r "$ROOT/frontend/dist/." "$ROOT/backend/web/dist/"

# ---------- 3/4 交叉编译 + 打包 ----------
rm -rf "$OUT"
mkdir -p "$OUT"

# build_plugins <GOOS> <GOARCH> <输出目录>
# 按 drivers.txt 列出的驱动、用 drivers.version 指定的版本，从模块代理拉取源码
# 并交叉编译成 iot-driver-<名字>。
#
# **插件是发行包的必需组成，不是可选项。** 全部协议驱动都已搬成插件、
# 主二进制里不再有任何协议实现：plugins/ 为空 = 网关不支持任何协议。
# 因此"一个插件都没编出来"在这里是致命错误 —— 否则会产出一个能启动、
# 能登录、却一台设备也采不到的发行包，而现场要到接设备时才发现。
#
# drivers.txt 每行一个驱动目录名（对应 iot-gateway-drivers 仓库的顶层目录），
# 产物名统一为 iot-driver-<目录名>。新增驱动时同时更新 drivers.txt。
#
# 用临时模块解析源码：`go install` 在设置 GOBIN 时禁止交叉编译，而 `go build`
# 需要一个主模块来解析依赖与 go.sum；`-o` 让产物名精确可控。
build_plugins() {
  local os="$1" arch="$2" outdir="$3" ext="" built=0 name ver buildtmp
  ver="$(tr -d '[:space:]' < "$ROOT/drivers.version")"
  [ -n "$ver" ] || { echo "  [FAIL] $ROOT/drivers.version 为空"; return 1; }
  [ "$os" = "windows" ] && ext=".exe"

  mkdir -p "$outdir"
  buildtmp="$(mktemp -d)"
  ( cd "$buildtmp" && GOWORK=off go mod init driverbuild >/dev/null 2>&1 )
  while IFS= read -r name; do
    [ -n "$name" ] || continue
    ( cd "$buildtmp" && \
      CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" GOWORK=off go get \
        "github.com/Ellisata/iot-gateway-drivers/${name}@${ver}" >/dev/null && \
      CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" GOWORK=off go build \
        -ldflags="-s -w -X main.version=${ver}" \
        -o "$outdir/iot-driver-${name}${ext}" \
        "github.com/Ellisata/iot-gateway-drivers/${name}" )
    echo "    [插件] ${name} ${ver} ($os/$arch)"
    built=$((built + 1))
  done < "$ROOT/drivers.txt"
  rm -rf "$buildtmp"

  if [ "$built" = "0" ]; then
    echo "  [FAIL] $os/$arch: 没有编译出任何插件 —— 发行包必须自带 plugins/，否则网关不支持任何协议"
    return 1
  fi
  echo "  [OK] $os/$arch: $built 个协议插件"
  return 0
}

# build_one <GOOS> <GOARCH>
build_one() {
  local os="$1" arch="$2" ext=""
  [ "$os" = "windows" ] && ext=".exe"

  echo "==> [3/4] 编译 $os/$arch ..."
  (cd "$ROOT/backend" && \
    CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -ldflags="-s -w" \
    -o "$OUT/iot-gateway$ext" .)

  # 进程外协议插件：与主二进制同平台交叉编译，打进压缩包的 plugins/ 目录。
  # 网关按 plugin.dir 扫描该目录（相对可执行文件解析），新增协议无需重编网关。
  # 编不出来就中止 —— 见 build_plugins 的说明。
  build_plugins "$os" "$arch" "$OUT/plugins" || exit 1

  if [ "$os" = "windows" ]; then
    # Windows: zip（二进制 + 插件 + 安装卸载脚本 + LICENSE）
    if [ -z "$ZIP_TAR" ]; then
      echo "[FAIL] 未找到可用的 zip 打包工具（需要 Windows 自带 tar.exe 或 bsdtar）"
      exit 1
    fi
    "$ZIP_TAR" -a -cf "$OUT/iot-gateway-${VERSION}-windows-${arch}.zip" \
      -C "$OUT" "iot-gateway.exe" "plugins" -C "$ROOT" install.bat uninstall.bat LICENSE
  else
    # Linux: tar.gz（--owner=0 归零属主，管理员解压不会出现异常 uid）
    "$TAR" czf "$OUT/iot-gateway-${VERSION}-${os}-${arch}.tar.gz" \
      --owner=0 --group=0 --numeric-owner \
      -C "$OUT" "iot-gateway" "plugins" -C "$ROOT" install.sh uninstall.sh LICENSE
  fi

  rm -f "$OUT/iot-gateway$ext"
  rm -rf "$OUT/plugins"
  echo "  [OK] $os/$arch 打包完成"
}

build_one windows amd64
build_one linux   amd64
build_one linux   arm64

# ---------- 4/4 生成 SHA256 校验 ----------
echo "==> [4/4] 生成 SHA256SUMS.txt ..."
( cd "$OUT" && sha256sum ./*.zip ./*.tar.gz > SHA256SUMS.txt )

echo
echo "============================================"
echo "  发行版打包完成: release/"
echo "============================================"
ls -lh "$OUT"
