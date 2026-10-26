#!/bin/sh
# ============================================================
#  iot-gateway Linux systemd 服务安装脚本
#  将脚本同目录下的 iot-gateway 二进制安装为系统后台服务
#
#  **刻意用 POSIX sh 而非 bash**: arm32 工控板多为 buildroot/busybox 系统,
#  板上要么没装 bash, 要么 /bin/sh 是 dash / busybox ash —— 这两者都不支持
#  `set -o pipefail`, 碰到 bash 版的 `set -euo pipefail` 会直接以
#  "Illegal option -o pipefail" 退出。本脚本不需要 pipefail(所有管道处都
#  显式写了 `|| true` / `if !`), 因此保持纯 POSIX 语法, 任何 /bin/sh 都能跑。
#
#  用法:
#    sudo ./install.sh                       安装并启动服务(默认 /opt/iot-gateway)
#    INSTALL_DIR=/opt/xxx sudo ./install.sh  自定义安装目录
#
#  服务名: iot-gateway
#  管理地址: http://<本机IP>:9082/admin
# ============================================================
set -eu

SERVICE_NAME="iot-gateway"
BINARY_NAME="iot-gateway"
INSTALL_DIR="${INSTALL_DIR:-/opt/iot-gateway}"
# 用 $0 而非 bash 专有的 BASH_SOURCE: `./install.sh` 与 `sh install.sh` 都成立
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
BINARY_PATH="${SCRIPT_DIR}/${BINARY_NAME}"
UNIT_FILE="/etc/systemd/system/${SERVICE_NAME}.service"

# 检查 root 权限
if [ "$(id -u)" -ne 0 ]; then
    echo "[FAIL] 需要 root 权限,请使用: sudo $0"
    exit 1
fi

# 检查可执行文件。除了发行包里的 iot-gateway(由 release.sh / package.bat 生成),
# 也认 build.sh 的交叉编译产物 iot-gateway-linux-arm / -arm64 —— arm32/arm64 工控板
# 通常是 `./build.sh arm32` 出文件后直接把二进制连插件目录拷到板子上, 不会重命名。
if [ ! -f "${BINARY_PATH}" ]; then
    for cand in "${SCRIPT_DIR}/${BINARY_NAME}-linux-arm" "${SCRIPT_DIR}/${BINARY_NAME}-linux-arm64"; do
        if [ -f "$cand" ]; then BINARY_PATH="$cand"; break; fi
    done
fi
if [ ! -f "${BINARY_PATH}" ]; then
    echo "[FAIL] 未找到 ${BINARY_NAME}(或 ${BINARY_NAME}-linux-arm / -arm64),请先执行 release.sh 或 build.sh 生成 Linux 可执行文件。"
    exit 1
fi

# 检查 systemd
if ! command -v systemctl >/dev/null 2>&1; then
    echo "[FAIL] 未检测到 systemd,本脚本仅支持 systemd 发行版。"
    exit 1
fi

echo "[1/3] 复制程序文件到 ${INSTALL_DIR} ..."
echo "  源二进制: $(basename "${BINARY_PATH}")"
mkdir -p "${INSTALL_DIR}/data" "${INSTALL_DIR}/log" "${INSTALL_DIR}/plugins"
cp -f "${BINARY_PATH}" "${INSTALL_DIR}/${BINARY_NAME}"
chmod +x "${INSTALL_DIR}/${BINARY_NAME}"
# 发行包清单：网关/SDK/驱动/线协议版本 + 插件 sha256。装到安装目录后仍可核对，
# 不必依赖解压出来的临时目录。
if [ -f "${SCRIPT_DIR}/MANIFEST.json" ]; then
    cp -f "${SCRIPT_DIR}/MANIFEST.json" "${INSTALL_DIR}/MANIFEST.json"
    echo "  [OK] 已复制 MANIFEST.json"
fi
# 若脚本同目录存在 default.yaml,一并复制(可覆盖嵌入的默认配置,如端口/日志级别)
if [ -f "${SCRIPT_DIR}/default.yaml" ]; then
    cp -f "${SCRIPT_DIR}/default.yaml" "${INSTALL_DIR}/default.yaml"
    echo "  [OK] 已复制 default.yaml"
fi
# 进程外协议插件(外部驱动)。网关按 plugin.dir 扫描此目录,**出厂即加载**
# (plugin.enabled 默认 true)—— 全部协议驱动都由插件提供、主程序不含任何内置实现,
# 该目录为空等于网关不支持任何协议(设备会被逐个静默跳过)。
# 加载插件等于以网关权限执行其中的程序,所以请确保该目录仅管理员可写。
# 插件目录必须跟二进制配对: 发行包里是 iot-gateway + plugins/, 而 build.sh 的产物
# 是 iot-gateway-linux-arm + plugins-linux-arm/。这一条很要紧 —— 无条件取 plugins/
# 时, 若它其实是 Windows 那套(*.exe, 仓库里叫 plugins-windows/), 就会把一堆 .exe
# 当驱动装进去, 装出一台"任何协议都不支持"的网关。
BINARY_SUFFIX="${BINARY_PATH##*/${BINARY_NAME}}"
PLUGIN_SRC="${SCRIPT_DIR}/plugins${BINARY_SUFFIX}"
[ -d "${PLUGIN_SRC}" ] || PLUGIN_SRC="${SCRIPT_DIR}/plugins"
if [ -d "${PLUGIN_SRC}" ]; then
    # 这里倘若放的是 Windows 插件(*.exe, 仓库里是 plugins-windows/)。真拷进去的话
    # 服务能起来、能登录, 但一台设备也采不到 —— 失败现场很安静, 所以这里点破它。
    if ls "${PLUGIN_SRC}"/*.exe >/dev/null 2>&1; then
        echo "  [!!] ${PLUGIN_SRC##*/} 里是 Windows 插件(.exe), Linux 网关加载不了 —— 确认是不是拷错了插件目录"
    fi
    cp -f "${PLUGIN_SRC}"/* "${INSTALL_DIR}/plugins/" 2>/dev/null || true
    chmod +x "${INSTALL_DIR}"/plugins/* 2>/dev/null || true
    echo "  [OK] 已复制协议插件 ($(ls -1 "${INSTALL_DIR}/plugins" | wc -l) 个,源目录 $(basename "${PLUGIN_SRC}"),已启用)"
else
    echo "  [!!] 同目录下找不到 plugins/ (或与二进制配套的 plugins-<平台>/),请确认文件是否齐全 —— 网关将不支持任何协议"
fi

echo "[2/3] 写入 systemd 服务单元 ..."
cat > "${UNIT_FILE}" <<EOF
[Unit]
Description=IoT Gateway
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
WorkingDirectory=${INSTALL_DIR}
ExecStart=${INSTALL_DIR}/${BINARY_NAME}
Restart=on-failure
RestartSec=5
# 如需通过 APP_ENV 加载 prod.yaml/dev.yaml,取消下行注释并在安装目录放置对应 yaml
# Environment=APP_ENV=prod
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload

echo "[3/3] 设置开机自启并启动服务 ..."
systemctl enable "${SERVICE_NAME}"
if ! systemctl restart "${SERVICE_NAME}"; then
    echo "[FAIL] 服务启动失败,查看日志: journalctl -u ${SERVICE_NAME} -e"
    exit 1
fi
sleep 1

echo ""
echo "============================================"
echo "  服务安装完成"
echo "  服务状态: $(systemctl is-active "${SERVICE_NAME}")"
echo "  管理地址: http://<本机IP>:9082/admin"
echo "  安装目录: ${INSTALL_DIR}"
echo "  数据目录: ${INSTALL_DIR}/data"
echo "  日志目录: ${INSTALL_DIR}/log"
echo "  插件目录: ${INSTALL_DIR}/plugins (全部协议驱动,已启用)"
echo "  常用命令:"
echo "    systemctl status ${SERVICE_NAME}    查看状态"
echo "    systemctl restart ${SERVICE_NAME}   重启服务"
echo "    journalctl -u ${SERVICE_NAME} -f    跟踪日志"
echo "============================================"
