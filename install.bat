@echo off
:: ============================================================
::  iot-gateway Windows 服务安装脚本（原生 sc，无需 NSSM）
::  将脚本同目录下的 iot-gateway.exe 注册为系统后台服务
::  （exe 内置 Windows 服务支持，见 backend/service_windows.go）
::
::  用法:
::    install.bat                          安装并启动服务（默认 %ProgramFiles%\iot-gateway）
::    set IOT_GATEWAY_HOME=C:\iot-gateway  自定义安装目录
::
::  服务名: iot-gateway
::  管理地址: http://localhost:9081/admin
:: ============================================================
setlocal
set "SERVICE_NAME=iot-gateway"
set "DISPLAY_NAME=IoT Gateway"
set "BINARY_NAME=iot-gateway.exe"
set "SRC_DIR=%~dp0"
set "INSTALL_DIR=%ProgramFiles%\iot-gateway"
if not "%IOT_GATEWAY_HOME%"=="" set "INSTALL_DIR=%IOT_GATEWAY_HOME%"

:: 检查管理员权限
net session >nul 2>&1
if errorlevel 1 (
    echo [FAIL] 需要管理员权限运行此脚本!
    pause
    exit /b 1
)

:: 检查可执行文件（由 backend\package.bat 生成）
if not exist "%SRC_DIR%%BINARY_NAME%" (
    echo [FAIL] 未找到 %BINARY_NAME%，请在 backend 目录执行 package.bat 生成。
    pause
    exit /b 1
)

echo.
echo [1/3] 复制程序文件到 %INSTALL_DIR% ...
if not exist "%INSTALL_DIR%" mkdir "%INSTALL_DIR%"
if not exist "%INSTALL_DIR%\data" mkdir "%INSTALL_DIR%\data"
if not exist "%INSTALL_DIR%\log"  mkdir "%INSTALL_DIR%\log"
copy /y "%SRC_DIR%%BINARY_NAME%" "%INSTALL_DIR%\%BINARY_NAME%" >nul
if errorlevel 1 (
    echo [FAIL] 复制可执行文件失败!
    pause
    exit /b 1
)
:: 脚本同目录若存在 default.yaml，一并复制（可覆盖嵌入的默认配置，如端口/日志级别）
if exist "%SRC_DIR%default.yaml" (
    copy /y "%SRC_DIR%default.yaml" "%INSTALL_DIR%\default.yaml" >nul
    echo   [OK] 已复制 default.yaml
)

echo [2/3] 注册 Windows 服务 ...
:: 若服务已存在，先停止并删除（重新安装）。ping 延迟等待 SCM 完成停止
sc stop   "%SERVICE_NAME%" >nul 2>&1
>nul 2>&1 ping -n 3 127.0.0.1
sc delete "%SERVICE_NAME%" >nul 2>&1

:: binPath 对含空格的路径需再加一层引号（sc 参数形式: binPath= "\"路径\""）
sc create "%SERVICE_NAME%" binPath= "\"%INSTALL_DIR%\%BINARY_NAME%\"" start= auto DisplayName= "%DISPLAY_NAME%"
if errorlevel 1 (
    echo [FAIL] 服务注册失败!
    pause
    exit /b 1
)
:: 崩溃自动重启（延迟 5 秒，最多尝试 3 次；24 小时内无故障则重置计数）
sc failure "%SERVICE_NAME%" reset= 86400 actions= restart/5000/restart/5000/restart/5000
sc description "%SERVICE_NAME%" "%DISPLAY_NAME% - 工业 IoT 数据采集网关"

echo [3/3] 启动服务 ...
sc start "%SERVICE_NAME%"
if errorlevel 1 (
    echo [FAIL] 服务启动失败，请查看日志目录: %INSTALL_DIR%\log
    pause
    exit /b 1
)

echo.
echo ============================================
echo   服务安装完成
echo   服务状态: 运行中（开机自启）
echo   管理地址: http://localhost:9081/admin
echo   安装目录: %INSTALL_DIR%
echo   数据目录: %INSTALL_DIR%\data
echo   日志目录: %INSTALL_DIR%\log
echo   常用命令:
echo     net start %SERVICE_NAME%     启动服务
echo     net stop %SERVICE_NAME%      停止服务
echo     sc query %SERVICE_NAME%      查看状态
echo     sc stop %SERVICE_NAME% ^&^& sc delete %SERVICE_NAME%   卸载服务
echo ============================================
endlocal
pause
exit /b 0
