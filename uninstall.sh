#!/bin/sh
# ============================================================
#  iot-gateway Linux systemd 服务卸载脚本
#
#  **刻意用 POSIX sh 而非 bash**: arm32 工控板多为 buildroot/busybox 系统,
#  板上要么没装 bash, 要么 /bin/sh 是 dash / busybox ash —— 这两者都不支持
#  `set -o pipefail`, 碰到 bash 版的 `set -euo pipefail` 会直接以
#  "Illegal option -o pipefail" 退出。本脚本不需要 pipefail, 因此保持纯
#  POSIX 语法, 任何 /bin/sh 都能跑。
#
#  用法:
#    sudo ./uninstall.sh            停止并卸载服务(保留安装目录与数据)
#    sudo ./uninstall.sh --purge    停止并卸载服务,同时删除安装目录(含数据)
# ============================================================
set -eu

SERVICE_NAME="iot-gateway"
INSTALL_DIR="${INSTALL_DIR:-/opt/iot-gateway}"
UNIT_FILE="/etc/systemd/system/${SERVICE_NAME}.service"

# 检查 root 权限
if [ "$(id -u)" -ne 0 ]; then
    echo "[FAIL] 需要 root 权限,请使用: sudo $0"
    exit 1
fi

echo "[1/2] 停止并禁用服务 ..."
if [ -f "${UNIT_FILE}" ]; then
    systemctl stop "${SERVICE_NAME}" 2>/dev/null || true
    systemctl disable "${SERVICE_NAME}" 2>/dev/null || true
    rm -f "${UNIT_FILE}"
    systemctl daemon-reload
    systemctl reset-failed "${SERVICE_NAME}" 2>/dev/null || true
    echo "  [OK] 服务单元已删除"
else
    echo "  [WARN] 未发现服务单元 ${UNIT_FILE},跳过"
fi

echo "[2/2] 清理安装目录"
if [ "${1:-}" = "--purge" ]; then
    rm -rf "${INSTALL_DIR}"
    echo "  [OK] 已删除安装目录 ${INSTALL_DIR} (含数据)"
else
    echo "  [INFO] 保留安装目录 ${INSTALL_DIR} 及数据。如需连数据一并删除: sudo $0 --purge"
fi

echo "[OK] 卸载完成"
