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

# build_one <GOOS> <GOARCH>
build_one() {
  local os="$1" arch="$2" ext=""
  [ "$os" = "windows" ] && ext=".exe"

  echo "==> [3/4] 编译 $os/$arch ..."
  (cd "$ROOT/backend" && \
    CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -ldflags="-s -w" \
    -o "$OUT/iot-gateway$ext" .)

  if [ "$os" = "windows" ]; then
    # Windows: zip（二进制 + 安装卸载脚本 + LICENSE）
    if [ -z "$ZIP_TAR" ]; then
      echo "[FAIL] 未找到可用的 zip 打包工具（需要 Windows 自带 tar.exe 或 bsdtar）"
      exit 1
    fi
    "$ZIP_TAR" -a -cf "$OUT/iot-gateway-${VERSION}-windows-${arch}.zip" \
      -C "$OUT" "iot-gateway.exe" -C "$ROOT" install.bat uninstall.bat LICENSE
  else
    # Linux: tar.gz（--owner=0 归零属主，管理员解压不会出现异常 uid）
    "$TAR" czf "$OUT/iot-gateway-${VERSION}-${os}-${arch}.tar.gz" \
      --owner=0 --group=0 --numeric-owner \
      -C "$OUT" "iot-gateway" -C "$ROOT" install.sh uninstall.sh LICENSE
  fi

  rm -f "$OUT/iot-gateway$ext"
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
