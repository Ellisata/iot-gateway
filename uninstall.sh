#!/usr/bin/env bash
# ============================================================
#  iot-hand-gateway Linux systemd 服务卸载脚本
#
#  用法:
#    sudo ./uninstall.sh            停止并卸载服务(保留安装目录与数据)
#    sudo ./uninstall.sh --purge    停止并卸载服务,同时删除安装目录(含数据)
# ============================================================
set -euo pipefail

SERVICE_NAME="iot-hand-gateway"
INSTALL_DIR="${INSTALL_DIR:-/opt/iot-hand-gateway}"
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
