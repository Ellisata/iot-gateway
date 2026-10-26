@echo off
:: Keep this file pure ASCII, comments included. cmd.exe parses batch files with
:: the console code page; non-ASCII (GBK or UTF-8) lines can desync its line
:: offsets and make it run the tail of a comment line as a command.
:: ============================================================
::  iot-gateway Windows service uninstaller
::
::  Usage:
::    uninstall.bat            stop and delete the service (keeps install dir and data)
::    uninstall.bat --purge    stop and delete the service, then remove the install dir
:: ============================================================
setlocal
set "SERVICE_NAME=iot-gateway"
set "SRC_DIR=%~dp0"
set "INSTALL_DIR=%ProgramFiles%\iot-gateway"
if not "%IOT_GATEWAY_HOME%"=="" set "INSTALL_DIR=%IOT_GATEWAY_HOME%"

:: Require administrator privileges
net session >nul 2>&1
if errorlevel 1 (
    echo [FAIL] This script must be run as Administrator.
    pause
    exit /b 1
)

echo [1/2] Stopping and deleting the service ...
sc stop   "%SERVICE_NAME%" >nul 2>&1
>nul 2>&1 ping -n 3 127.0.0.1
sc delete "%SERVICE_NAME%" >nul 2>&1
if errorlevel 1 echo   [WARN] Service may not exist or deletion failed; safe to ignore.

echo [2/2] Handling the install directory
if /i "%1"=="--purge" (
    if exist "%INSTALL_DIR%" (
        echo [INFO] Removing install directory %INSTALL_DIR% ...
        rmdir /s /q "%INSTALL_DIR%"
    )
    echo [OK] Install directory removed.
) else (
    echo [INFO] Keeping install directory %INSTALL_DIR% and its data. To remove it: uninstall.bat --purge
)

echo [OK] Uninstall complete
endlocal
pause
exit /b 0
