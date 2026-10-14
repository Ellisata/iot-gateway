@echo off
:: 多平台打包脚本 - Linux amd64 / Linux arm64 / Linux arm(32) / Windows amd64
:: ============================================
:: 统一切到脚本所在目录，这样从任何位置调用都成立（%~dp0 而非调用者 cwd）。
:: 用 pushd 而不是 cd /d：UNC 路径（网络共享）下 cd /d 会直接失败。
::
:: 用法:
::   package.bat           全量: Linux amd64 + Windows amd64 + Linux arm64 + Linux arm32
::   package.bat linux     仅 Linux amd64
::   package.bat windows   仅 Windows amd64
::   package.bat arm64     仅 Linux arm64
::   package.bat arm32     仅 Linux arm 32 位（固定 GOARM=7）
::
:: 产物落在仓库根目录（本脚本的上一级）:
::   iot-gateway              + plugins-linux\        Linux amd64
::   iot-gateway-linux-arm64  + plugins-linux-arm64\  Linux arm64
::   iot-gateway-linux-arm    + plugins-linux-arm\    Linux arm32（GOARM=7）
::   iot-gateway.exe          + plugins-windows\      Windows amd64
::
::  注意: 插件目录一律带平台后缀，Windows 是 plugins-windows\；网关 plugin.dir
::        默认是 "plugins"、相对 exe 解析。本地直接跑 exe 之前要把对应目录改名成
::        plugins\ 或改 plugin.dir，否则一个协议也扫不到。发行包不受影响。
::
:: arm32 固定 GOARM=7（ARMv7-A + VFPv3 硬浮点）。目标板若是 ARMv6/ARMv5
:: （树莓派 1 / Zero 一代）得改用 GOARM=5 软浮点，否则产物在板子上直接起不来。
pushd "%~dp0"

set PKG_TARGET=%1
if "%PKG_TARGET%"=="" set PKG_TARGET=all

set DO_LINUX=0
set DO_WINDOWS=0
set DO_ARM64=0
set DO_ARM32=0

if /i "%PKG_TARGET%"=="all" (
    set DO_LINUX=1
    set DO_WINDOWS=1
    set DO_ARM64=1
    set DO_ARM32=1
)
if /i "%PKG_TARGET%"=="linux" set DO_LINUX=1
if /i "%PKG_TARGET%"=="windows" set DO_WINDOWS=1
if /i "%PKG_TARGET%"=="win" set DO_WINDOWS=1
if /i "%PKG_TARGET%"=="arm64" set DO_ARM64=1
if /i "%PKG_TARGET%"=="arm32" set DO_ARM32=1
if /i "%PKG_TARGET%"=="arm7" set DO_ARM32=1
rem arm 按 Go 的惯例指 32 位（GOARCH=arm），64 位用 arm64。
if /i "%PKG_TARGET%"=="arm" set DO_ARM32=1

if "%DO_LINUX%%DO_WINDOWS%%DO_ARM64%%DO_ARM32%"=="0000" (
    echo [FAIL] 未知目标: %PKG_TARGET%   可用: all / linux / windows / arm64 / arm32
    popd
    exit /b 1
)

if "%DO_LINUX%"=="1" (
    call :build_one linux amd64 iot-gateway plugins-linux
    if errorlevel 1 goto :fail
)
if "%DO_WINDOWS%"=="1" (
    call :build_one windows amd64 iot-gateway.exe plugins-windows
    if errorlevel 1 goto :fail
)
if "%DO_ARM64%"=="1" (
    call :build_one linux arm64 iot-gateway-linux-arm64 plugins-linux-arm64
    if errorlevel 1 goto :fail
)
if "%DO_ARM32%"=="1" (
    call :build_one linux arm iot-gateway-linux-arm plugins-linux-arm 7
    if errorlevel 1 goto :fail
)

:: Done
:: ====
echo.
echo ========================================
echo   All builds completed successfully!
echo ========================================
if "%DO_LINUX%"=="1"   echo   - iot-gateway             (linux/amd64)
if "%DO_WINDOWS%"=="1" echo   - iot-gateway.exe         (windows/amd64)
if "%DO_ARM64%"=="1"   echo   - iot-gateway-linux-arm64 (linux/arm64)
if "%DO_ARM32%"=="1"   echo   - iot-gateway-linux-arm   (linux/arm, GOARM=7)
echo ========================================
:: 主流程到此为止。少了这行，执行会「掉进」下面的 :build_one / :build_plugins
:: 标签，用空的 %~1/%~2 再编一遍插件，%PB_OUT% 为空时输出路径退化成盘根。
popd
exit /b 0

:fail
echo.
echo [FAIL] 构建失败，请查看上方日志。
popd
exit /b 1


:: build_one <GOOS> <GOARCH> <主二进制名> <插件目录名> [GOARM]
:: 主二进制与同名 plugins-* 目录都落在仓库根（本脚本的上一级）。
:build_one
setlocal
set BO_GOOS=%1
set BO_ARCH=%2
set BO_BIN=%~3
set BO_PLUGINS=%~4
set BO_GOARM=%5
set BO_TAG=%BO_ARCH%
if not "%BO_GOARM%"=="" set BO_TAG=%BO_ARCH%/%BO_GOARM%

echo.
echo ========================================
echo   Compiling %BO_GOOS%/%BO_TAG% ...
echo ========================================
echo GOOS=%BO_GOOS%  GOARCH=%BO_ARCH%  CGO_ENABLED=0
if not "%BO_GOARM%"=="" echo GOARM=%BO_GOARM%

set CGO_ENABLED=0
set GOOS=%BO_GOOS%
set GOARCH=%BO_ARCH%
set GOARM=%BO_GOARM%

go build -ldflags="-s -w" -o "..\%BO_BIN%" .
if errorlevel 1 (
    echo [FAIL] %BO_GOOS%/%BO_TAG% build failed!
    endlocal & exit /b 1
)

:: 进程外协议插件：与主二进制同平台**同 GOARM** 交叉编译，落在仓库根的同名 plugins-* 目录。
:: 编不出来就中止 —— 见 :build_plugins 的说明。
call :build_plugins %BO_GOOS% %BO_ARCH% "%~dp0..\%BO_PLUGINS%" %BO_GOARM%
if errorlevel 1 (
    endlocal & exit /b 1
)

for /f %%i in ('powershell -Command "Get-ChildItem ..\%BO_BIN% ^| ForEach-Object { [math]::Round($_.Length/1MB, 2) }"') do (
    echo [OK] %BO_BIN% built successfully! Size: %%i MB
)
endlocal & exit /b 0


:: build_plugins <GOOS> <GOARCH> <OUTDIR> [GOARM]
:: Cross-compile every backend\driversrc\* plugin into OUTDIR as iot-driver-<name>.
::
:: Plugins are NOT optional: all protocol drivers live in plugins now, the main
:: binary has no built-in protocol left. An empty plugins dir means the gateway
:: supports nothing, so a failure here is fatal.
::
:: NOTE: the plugin binaries must match the main binary's GOOS/GOARCH/GOARM, so
:: those are parameters here -- building arm64 plugins as amd64 (or a GOARM=7
:: plugin next to a GOARM=5 host) yields a gateway that silently loads nothing
:: and skips every device.
:build_plugins
setlocal enabledelayedexpansion
set PB_GOOS=%1
set PB_ARCH=%2
set PB_OUT=%~3
set PB_GOARM=%4
set PB_EXT=
if /i "%PB_GOOS%"=="windows" set PB_EXT=.exe
if not exist "%PB_OUT%" mkdir "%PB_OUT%"
set PB_BUILT=0
for /d %%d in (driversrc\*) do (
    if exist "%%d\main.go" (
        for %%n in ("%%d") do set PB_NAME=%%~nn
        set CGO_ENABLED=0
        set GOOS=%PB_GOOS%
        set GOARCH=%PB_ARCH%
        set GOARM=%PB_GOARM%
        go build -ldflags="-s -w" -o "%PB_OUT%\iot-driver-!PB_NAME!%PB_EXT%" ".\%%d"
        if errorlevel 1 (
            echo [FAIL] plugin !PB_NAME! build failed for %PB_GOOS%/%PB_ARCH%
            endlocal & exit /b 1
        )
        echo     [OK] iot-driver-!PB_NAME!%PB_EXT%
        set /a PB_BUILT+=1
    )
)
if %PB_BUILT%==0 (
    echo [FAIL] no protocol plugin built -- the package must ship plugins\
    endlocal & exit /b 1
)
endlocal & exit /b 0
