@echo off
:: ============================================================
::  iot-gateway Windows 服务卸载脚本
::
::  用法:
::    uninstall.bat           停止并删除服务（保留安装目录及数据）
::    uninstall.bat --purge   停止并删除服务，同时删除安装目录（慎用）
:: ============================================================
setlocal
set "SERVICE_NAME=iot-gateway"
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

echo [1/2] 停止并删除服务 ...
sc stop   "%SERVICE_NAME%" >nul 2>&1
>nul 2>&1 ping -n 3 127.0.0.1
sc delete "%SERVICE_NAME%" >nul 2>&1
if errorlevel 1 echo   [WARN] 服务可能不存在或删除失败，可忽略。

echo [2/2] 处理安装目录
if /i "%1"=="--purge" (
    if exist "%INSTALL_DIR%" (
        echo [INFO] 删除安装目录 %INSTALL_DIR% ...
        rmdir /s /q "%INSTALL_DIR%"
    )
    echo [OK] 已删除安装目录。
) else (
    echo [INFO] 保留安装目录 %INSTALL_DIR% 及其数据。若要删除: uninstall.bat --purge
)

echo [OK] 卸载完成
endlocal
pause
exit /b 0
