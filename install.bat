@echo off
:: Keep this file pure ASCII, comments included. cmd.exe parses batch files with
:: the console code page; non-ASCII (GBK or UTF-8) lines can desync its line
:: offsets and make it run the tail of a comment line as a command.
:: ============================================================
::  iot-gateway Windows service installer (native sc, no NSSM)
::  Registers the iot-gateway.exe sitting next to this script as a system service.
::  (The exe has built-in Windows service support, see backend/service_windows.go)
::
::  Usage:
::    install.bat                          install and start (default dir: %ProgramFiles%\iot-gateway)
::    set IOT_GATEWAY_HOME=C:\iot-gateway  custom install directory
::
::  Service name: iot-gateway
::  Console: http://localhost:9082/admin
:: ============================================================
setlocal
set "SERVICE_NAME=iot-gateway"
set "DISPLAY_NAME=IoT Gateway"
set "BINARY_NAME=iot-gateway.exe"
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

:: Require the executable (produced by backend\package.bat)
if not exist "%SRC_DIR%%BINARY_NAME%" (
    echo [FAIL] %BINARY_NAME% not found. Build it first: run package.bat in backend.
    pause
    exit /b 1
)

echo.
echo [1/3] Copying program files to %INSTALL_DIR% ...
if not exist "%INSTALL_DIR%" mkdir "%INSTALL_DIR%"
if not exist "%INSTALL_DIR%\data" mkdir "%INSTALL_DIR%\data"
if not exist "%INSTALL_DIR%\log"  mkdir "%INSTALL_DIR%\log"
if not exist "%INSTALL_DIR%\plugins" mkdir "%INSTALL_DIR%\plugins"
copy /y "%SRC_DIR%%BINARY_NAME%" "%INSTALL_DIR%\%BINARY_NAME%" >nul
if errorlevel 1 (
    echo [FAIL] Copying the executable failed.
    pause
    exit /b 1
)
:: Release manifest (versions + plugin checksums), kept for field checks.
if exist "%SRC_DIR%MANIFEST.json" (
    copy /y "%SRC_DIR%MANIFEST.json" "%INSTALL_DIR%\MANIFEST.json" >nul
    echo   [OK] MANIFEST.json copied
)
:: Copy default.yaml too when it sits next to this script; it overrides the
:: embedded defaults (port, log level, ...).
if exist "%SRC_DIR%default.yaml" (
    copy /y "%SRC_DIR%default.yaml" "%INSTALL_DIR%\default.yaml" >nul
    echo   [OK] default.yaml copied
)
:: Out-of-process protocol plugins (external drivers). The gateway scans this
:: dir via plugin.dir and loads it out of the box (plugin.enabled defaults to
:: true). Every protocol driver is a plugin and the main binary contains no
:: built-in protocol, so an empty dir means the gateway supports nothing (each
:: device gets silently skipped).
:: Loading a plugin runs it with the gateway's privileges -- keep this dir
:: writable by administrators only.
:: The dir resolves relative to the exe location, independent of the working
:: directory sc create uses.
if exist "%SRC_DIR%plugins" (
    xcopy /y /e /i /q "%SRC_DIR%plugins" "%INSTALL_DIR%\plugins" >nul
    echo   [OK] Protocol plugins copied, enabled
)

echo [2/3] Registering the Windows service ...
:: If the service already exists, stop and delete it first (reinstall). The
:: ping delay gives the SCM time to finish stopping.
sc stop   "%SERVICE_NAME%" >nul 2>&1
>nul 2>&1 ping -n 3 127.0.0.1
sc delete "%SERVICE_NAME%" >nul 2>&1

:: binPath needs an extra layer of quotes when the path contains spaces
:: (sc syntax: binPath= "\"path\"")
sc create "%SERVICE_NAME%" binPath= "\"%INSTALL_DIR%\%BINARY_NAME%\"" start= auto DisplayName= "%DISPLAY_NAME%"
if errorlevel 1 (
    echo [FAIL] Service registration failed.
    pause
    exit /b 1
)
:: Auto-restart on crash (5s delay, up to 3 attempts; counter resets after 24h)
sc failure "%SERVICE_NAME%" reset= 86400 actions= restart/5000/restart/5000/restart/5000
sc description "%SERVICE_NAME%" "%DISPLAY_NAME% - industrial IoT data collection gateway"

echo [3/3] Starting the service ...
sc start "%SERVICE_NAME%"
if errorlevel 1 (
    echo [FAIL] Starting the service failed. Check the log dir: %INSTALL_DIR%\log
    pause
    exit /b 1
)

echo.
echo ============================================
echo   Service installed
echo   Status   : running (auto-start on boot)
echo   Console  : http://localhost:9082/admin
echo   Install  : %INSTALL_DIR%
echo   Data     : %INSTALL_DIR%\data
echo   Logs     : %INSTALL_DIR%\log
echo   Plugins  : %INSTALL_DIR%\plugins (all protocol drivers, enabled)
echo   Commands :
echo     net start %SERVICE_NAME%     start the service
echo     net stop %SERVICE_NAME%      stop the service
echo     sc query %SERVICE_NAME%      query status
echo     sc stop %SERVICE_NAME% ^&^& sc delete %SERVICE_NAME%   uninstall
echo ============================================
endlocal
pause
exit /b 0
