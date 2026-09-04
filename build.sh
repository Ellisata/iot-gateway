#!/usr/bin/env bash
# ============================================
#  iot-gateway 一键构建脚本（Linux / macOS）
#  前端构建 -> 同步到 backend/web/dist -> Go 交叉编译
#
#  用法:
#    ./build.sh            # 构建 Linux + Windows 双平台产物（默认）
#    ./build.sh windows    # 仅构建 Windows
#    ./build.sh linux      # 仅构建 Linux
#    ./build.sh native     # 仅构建当前平台
#
#  产物: backend/iot-gateway(.exe)
# ============================================
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

TARGET="${1:-all}"
case "$TARGET" in
  windows) BUILD_LINUX=0; BUILD_WINDOWS=1; BUILD_NATIVE=0 ;;
  win)     BUILD_LINUX=0; BUILD_WINDOWS=1; BUILD_NATIVE=0 ;;
  linux)   BUILD_LINUX=1; BUILD_WINDOWS=0; BUILD_NATIVE=0 ;;
  native)  BUILD_LINUX=0; BUILD_WINDOWS=0; BUILD_NATIVE=1 ;;
  all)     BUILD_LINUX=1; BUILD_WINDOWS=1; BUILD_NATIVE=0 ;;
  *) echo "unknown target: $TARGET (use all | windows | linux | native)"; exit 1 ;;
esac

echo
echo "[1/3] building frontend (npm run build) ..."
cd "$ROOT/frontend"
if [ ! -d node_modules ]; then
  echo "  node_modules not found, running npm install ..."
  npm install
fi
npm run build

echo "[2/3] syncing artifacts to backend/web/dist ..."
rm -rf "$ROOT/backend/web/dist"
mkdir -p "$ROOT/backend/web/dist"
cp -r "$ROOT/frontend/dist/." "$ROOT/backend/web/dist/"

echo "[3/3] compiling Go backend ..."
cd "$ROOT/backend"

build_one() { # $1=GOOS $2=GOARCH $3=output-name
  echo
  echo "  Compiling $1/$2 ..."
  CGO_ENABLED=0 GOOS="$1" GOARCH="$2" go build -ldflags="-s -w" -o "$3" .
  local size
  size=$(du -m "$3" | cut -f1)
  echo "  [OK] $3 built ($size MB)"
}

if [ "$BUILD_LINUX" = "1" ]; then
  build_one linux  amd64 iot-gateway
fi
if [ "$BUILD_WINDOWS" = "1" ]; then
  build_one windows amd64 iot-gateway.exe
fi
if [ "$BUILD_NATIVE" = "1" ]; then
  if [ "$(uname -s)" = "Windows_NT" ] || [[ "${OS:-}" == "Windows_NT" ]]; then
    build_one windows amd64 iot-gateway.exe
  else
    build_one "$(uname -s | tr '[:upper:]' '[:lower:]')" amd64 iot-gateway
  fi
fi

echo
echo "============================================"
echo "  Build completed successfully!"
echo "  Run: ./iot-gateway   Console: http://localhost:9081/admin"
echo "============================================"
