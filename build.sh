#!/usr/bin/env bash
# ============================================
#  iot-gateway 一键构建脚本（Linux / macOS）
#  前端构建 -> 同步到 backend/web/dist -> Go 交叉编译
#
#  用法:
#    ./build.sh            # 构建 Linux amd64 + Linux arm64 + Linux arm(32) + Windows（默认）
#    ./build.sh windows    # 仅构建 Windows amd64
#    ./build.sh linux      # 仅构建 Linux amd64
#    ./build.sh arm64      # 仅构建 Linux arm64（ARM64 工控机 / 树莓派等）
#    ./build.sh arm32      # 仅构建 Linux arm 32 位（ARMv7 工控板）
#    ./build.sh native     # 仅构建当前平台
#
#  产物: backend/ 下，每个平台各一套「主二进制 + 同名插件目录」，见 plugin_dir_for()
#    iot-gateway             + plugins-linux/
#    iot-gateway-linux-arm64 + plugins-linux-arm64/
#    iot-gateway-linux-arm   + plugins-linux-arm/
#    iot-gateway.exe         + plugins-windows/
#
#  arm32 固定按 GOARM=7（ARMv7-A + VFPv3 硬浮点）编译。若目标板是 ARMv6/ARMv5
#  （如树莓派 1 / Zero 一代）得改 GOARM=5 用软浮点，否则产物在板子上直接起不来。
#
#  注意: 网关按 plugin.dir(默认 "plugins") 相对 **可执行文件所在目录** 解析插件路径，
#  而这里每个平台产出的都是 plugins-<平台>/(Windows 是 plugins-windows/)，
#  所以跑之前要把该目录改名成 plugins/，或把 plugin.dir 指过去。发行包不受影响:
#  release.sh 是独立打包流程，压缩包里就是 plugins/ 这一层。
# ============================================
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

TARGET="${1:-all}"
case "$TARGET" in
  windows)    BUILD_LINUX=0; BUILD_WINDOWS=1; BUILD_ARM64=0; BUILD_ARM32=0; BUILD_NATIVE=0 ;;
  win)        BUILD_LINUX=0; BUILD_WINDOWS=1; BUILD_ARM64=0; BUILD_ARM32=0; BUILD_NATIVE=0 ;;
  linux)      BUILD_LINUX=1; BUILD_WINDOWS=0; BUILD_ARM64=0; BUILD_ARM32=0; BUILD_NATIVE=0 ;;
  arm64)      BUILD_LINUX=0; BUILD_WINDOWS=0; BUILD_ARM64=1; BUILD_ARM32=0; BUILD_NATIVE=0 ;;
  arm32|arm7) BUILD_LINUX=0; BUILD_WINDOWS=0; BUILD_ARM64=0; BUILD_ARM32=1; BUILD_NATIVE=0 ;;
  arm)        BUILD_LINUX=0; BUILD_WINDOWS=0; BUILD_ARM64=0; BUILD_ARM32=1; BUILD_NATIVE=0 ;;
  native)     BUILD_LINUX=0; BUILD_WINDOWS=0; BUILD_ARM64=0; BUILD_ARM32=0; BUILD_NATIVE=1 ;;
  all)        BUILD_LINUX=1; BUILD_WINDOWS=1; BUILD_ARM64=1; BUILD_ARM32=1; BUILD_NATIVE=0 ;;
  *) echo "unknown target: $TARGET (use all | windows | linux | arm64 | arm32 | native)"; exit 1 ;;
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

plugin_dir_for() { # $1=GOOS $2=GOARCH
  # 一律带平台后缀——多平台产物文件名相同（Windows 是 iot-driver-x.exe，其余是
  # iot-driver-x），共用一个目录会互相覆盖成错的架构，且覆盖后不报任何错。
  # 代价：产物目录名不再等于网关默认的 plugin.dir("plugins")，本地直跑 exe 之前
  # 要把 plugins-<平台>/ 改名成 plugins/，或把 plugin.dir 指到这个目录。
  if [ "$2" = "amd64" ]; then
    echo "plugins-$1"
  else
    echo "plugins-$1-$2"
  fi
}

build_one() { # $1=GOOS $2=GOARCH $3=output-name [$4=GOARM，仅 GOARCH=arm 需要]
  # GOARM 只对 GOARCH=arm 生效，且**必须**跟 GOARCH=arm 一起给：漏了它 Go 会按自带的
  # 默认值(7)编，要软浮点通用版就得显式传 5。用 env 逐条注入而不是 export，
  # 免得上一次调用的 GOARM 泄漏到下一个平台(会被 Go 静默忽略，但产物语义就说不清了)。
  local -a genv=(CGO_ENABLED=0 GOOS="$1" GOARCH="$2")
  [ -n "${4:-}" ] && genv+=(GOARM="$4")
  local arch="$2"
  [ -n "${4:-}" ] && arch="$2/$4"

  echo
  echo "  Compiling $1/$arch ..."
  env "${genv[@]}" go build -ldflags="-s -w" -o "$3" .
  local size
  size=$(du -m "$3" | cut -f1)
  echo "  [OK] $3 built ($size MB)"

  # 外部协议驱动插件(进程外驱动):与主二进制同平台编译到 backend/<平台专属目录>/。
  # 目录必须与主二进制同层——网关按 plugin.dir 相对 os.Executable() 解析
  # (pluginhost.resolvePluginDir),两者不在同一层就跑起来扫不到。
  # 目录名按平台区分见 plugin_dir_for()：多个平台同名文件写同一个目录会互相覆盖，
  # 覆盖完不会报任何错，只会得到一个插件架构不对、设备全被静默跳过的网关。
  # 插件也必须跟主二进制同 GOARM —— 这里复用同一个 genv，别另起一份环境。
  # **插件是必需的**:全部协议驱动都已搬成插件、主程序不含任何内置实现,
  # 插件目录为空 = 网关不支持任何协议(设备会被逐个静默跳过)。
  local ext="" built=0
  local pdir dir
  pdir="$(plugin_dir_for "$1" "$2")"
  dir="$ROOT/backend/$pdir"
  [ "$1" = "windows" ] && ext=".exe"
  for d in driversrc/*/; do
    [ -f "${d}main.go" ] || continue
    local name
    name="$(basename "$d")"
    mkdir -p "$dir"
    env "${genv[@]}" go build -ldflags="-s -w" \
      -o "$dir/iot-driver-${name}${ext}" "./${d}"
    echo "    [OK] ${pdir}/iot-driver-${name}${ext}"
    built=$((built + 1))
  done

  # 一个插件都没编出来就是环境坏了(没有 driversrc/*/main.go,或全部编译失败)。
  # 这里必须失败:主程序不含任何内置协议,插件目录为空 = 网关不支持任何协议。
  if [ "$built" = "0" ]; then
    echo "  [FAIL] $1/$2: 没有编译出任何插件 —— 网关将不支持任何协议"
    return 1
  fi
}


if [ "$BUILD_LINUX" = "1" ]; then
  build_one linux   amd64 iot-gateway
fi
if [ "$BUILD_ARM64" = "1" ]; then
  build_one linux   arm64 iot-gateway-linux-arm64
fi
if [ "$BUILD_ARM32" = "1" ]; then
  build_one linux   arm   iot-gateway-linux-arm 7
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
echo "  Run: ./iot-gateway   Console: http://localhost:9082/admin"
echo "  (跑之前先把 plugins-<平台>/ 改名成 plugins/，否则扫不到任何协议)"
echo "============================================"
