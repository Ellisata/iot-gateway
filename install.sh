#!/usr/bin/env bash
# ============================================================
#  iot-hand-gateway Linux systemd 服务安装脚本
#  将脚本同目录下的 iot-hand-gateway 二进制安装为系统后台服务
#
#  用法:
#    sudo ./install.sh                       安装并启动服务(默认 /opt/iot-hand-gateway)
#    INSTALL_DIR=/opt/xxx sudo ./install.sh  自定义安装目录
#
#  服务名: iot-hand-gateway
#  管理地址: http://<本机IP>:9081/admin
# ============================================================
set -euo pipefail

SERVICE_NAME="iot-hand-gateway"
BINARY_NAME="iot-hand-gateway"
INSTALL_DIR="${INSTALL_DIR:-/opt/iot-hand-gateway}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BINARY_PATH="${SCRIPT_DIR}/${BINARY_NAME}"
UNIT_FILE="/etc/systemd/system/${SERVICE_NAME}.service"

# 检查 root 权限
if [ "$(id -u)" -ne 0 ]; then
    echo "[FAIL] 需要 root 权限,请使用: sudo $0"
    exit 1
fi

# 检查可执行文件(由 backend/package.bat 生成)
if [ ! -f "${BINARY_PATH}" ]; then
    echo "[FAIL] 未找到 ${BINARY_NAME},请先在 backend 目录执行 package.bat 生成 Linux 可执行文件。"
    exit 1
fi

# 检查 systemd
if ! command -v systemctl >/dev/null 2>&1; then
    echo "[FAIL] 未检测到 systemd,本脚本仅支持 systemd 发行版。"
    exit 1
fi

echo "[1/3] 复制程序文件到 ${INSTALL_DIR} ..."
mkdir -p "${INSTALL_DIR}/data" "${INSTALL_DIR}/log"
cp -f "${BINARY_PATH}" "${INSTALL_DIR}/${BINARY_NAME}"
chmod +x "${INSTALL_DIR}/${BINARY_NAME}"
# 若脚本同目录存在 default.yaml,一并复制(可覆盖嵌入的默认配置,如端口/日志级别)
if [ -f "${SCRIPT_DIR}/default.yaml" ]; then
    cp -f "${SCRIPT_DIR}/default.yaml" "${INSTALL_DIR}/default.yaml"
    echo "  [OK] 已复制 default.yaml"
fi

echo "[2/3] 写入 systemd 服务单元 ..."
cat > "${UNIT_FILE}" <<EOF
[Unit]
Description=IoT Hand Gateway
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
echo "  管理地址: http://<本机IP>:9081/admin"
echo "  安装目录: ${INSTALL_DIR}"
echo "  数据目录: ${INSTALL_DIR}/data"
echo "  日志目录: ${INSTALL_DIR}/log"
echo "  常用命令:"
echo "    systemctl status ${SERVICE_NAME}    查看状态"
echo "    systemctl restart ${SERVICE_NAME}   重启服务"
echo "    journalctl -u ${SERVICE_NAME} -f    跟踪日志"
echo "============================================"
