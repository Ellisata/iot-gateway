@echo off
:: ============================================
::  iot-gateway 生产模式一键构建
::  前端构建 -> 同步到 backend/web/dist -> Go 编译
::  用法: build.bat          编译 Linux amd64 + arm64 + arm32 + Windows amd64（部署用，默认）
::        build.bat windows  仅编译 Windows amd64（本机原生，最快）
::        build.bat arm64    仅编译 Linux arm64（ARM64 工控机 / 树莓派等）
::        build.bat arm32    仅编译 Linux arm 32 位（ARMv7 工控板，固定 GOARM=7）
::  产物: 默认模式调 backend\package.bat，产物落在仓库根目录:
::          iot-gateway / iot-gateway-linux-arm64 / iot-gateway-linux-arm / iot-gateway.exe
::          plugins-linux\ / plugins-linux-arm64\ / plugins-linux-arm\ / plugins-windows\
::        单平台模式产物在 backend\ 下，便于就地调试
::
::  注意: 插件目录一律带平台后缀，Windows 是 plugins-windows\；网关 plugin.dir
::        默认是 "plugins"、相对 exe 解析。本地直接跑 exe 之前要把对应目录改名成
::        plugins\ 或改 plugin.dir，否则一个协议也扫不到。发行包不受影响。
::
::  arm32 固定 GOARM=7（ARMv7-A + VFPv3 硬浮点）。目标板若是 ARMv6/ARMv5
::  （树莓派 1 / Zero 一代）得改用 GOARM=5 软浮点，否则产物在板子上直接起不来。
:: ============================================
setlocal
set ROOT=%~dp0

:: all（默认，全量） / windows / arm64 / arm32
set BUILD_ALL=1
set BUILD_ARM=0
set BUILD_ARM32=0
if /i "%1"=="windows" (
    set BUILD_ALL=0
    set BUILD_ARM=0
    set BUILD_ARM32=0
)
if /i "%1"=="win" (
    set BUILD_ALL=0
    set BUILD_ARM=0
    set BUILD_ARM32=0
)
if /i "%1"=="arm64" (
    set BUILD_ALL=0
    set BUILD_ARM=1
    set BUILD_ARM32=0
)
if /i "%1"=="arm32" (
    set BUILD_ALL=0
    set BUILD_ARM=0
    set BUILD_ARM32=1
)
if /i "%1"=="arm7" (
    set BUILD_ALL=0
    set BUILD_ARM=0
    set BUILD_ARM32=1
)
rem arm 按 Go 的惯例指 32 位（GOARCH=arm），64 位用 arm64。
if /i "%1"=="arm" (
    set BUILD_ALL=0
    set BUILD_ARM=0
    set BUILD_ARM32=1
)

echo.
echo [1/3] 构建前端 (npm run build) ...
pushd "%ROOT%frontend"
call npm run build
if errorlevel 1 goto :fail

echo [2/3] 同步构建产物到 backend\web\dist ...
if exist "%ROOT%backend\web\dist" (
    rmdir /s /q "%ROOT%backend\web\dist"
)
mkdir "%ROOT%backend\web\dist"
xcopy /e /y /q "%ROOT%frontend\dist\*" "%ROOT%backend\web\dist\" >nul
if errorlevel 1 goto :fail

echo [3/3] 编译 Go 后端 ...
pushd "%ROOT%backend"

if "%BUILD_ALL%"=="0" (
    if "%BUILD_ARM%"=="1" (
        call :build_arm64
    ) else if "%BUILD_ARM32%"=="1" (
        call :build_arm32
    ) else (
        call :build_windows
    )
    if errorlevel 1 goto :fail
) else (
    rem 全量编译交给 package.bat：架构清单与插件 glob 只留那一处，免得两边漂移。
    call "%ROOT%backend\package.bat"
    if errorlevel 1 goto :fail
)

echo.
echo ============================================
if "%BUILD_ALL%"=="0" (
  if "%BUILD_ARM%"=="1" (
    echo   构建完成:
    echo   - Linux arm64 : %ROOT%backend\iot-gateway-linux-arm64
    echo   - 插件目录    : %ROOT%backend\plugins-linux-arm64
  ) else if "%BUILD_ARM32%"=="1" (
    echo   构建完成:
    echo   - Linux arm32 : %ROOT%backend\iot-gateway-linux-arm
    echo   - 插件目录    : %ROOT%backend\plugins-linux-arm
  ) else (
    echo   构建完成:
    echo   - Windows     : %ROOT%backend\iot-gateway.exe
    echo   - 插件目录    : %ROOT%backend\plugins-windows
  )
) else (
  echo   构建完成（产物在仓库根目录）:
  echo   - Linux amd64  : %ROOT%iot-gateway
  echo   - Linux arm64  : %ROOT%iot-gateway-linux-arm64
  echo   - Linux arm32  : %ROOT%iot-gateway-linux-arm
  echo   - Windows amd64: %ROOT%iot-gateway.exe
)
echo   运行方式: 双击 run.bat 或直接运行 iot-gateway.exe
echo   访问地址: http://localhost:9082/admin
echo ============================================
endlocal
exit /b 0

:fail
echo.
echo [FAIL] 构建失败，请查看上方日志。
endlocal
exit /b 1

:build_windows
echo.
echo   Compiling Windows 64-bit...
set CGO_ENABLED=0
set GOOS=windows
set GOARCH=amd64
set GOARM=
go build -ldflags="-s -w" -o iot-gateway.exe .
if errorlevel 1 (
    echo [FAIL] Windows build failed!
    exit /b 1
)
for /f %%i in ('powershell -Command "Get-ChildItem iot-gateway.exe ^| ForEach-Object { [math]::Round($_.Length/1MB, 2) }"') do (
    echo [OK] Windows build successful! Size: %%i MB
)
call :build_plugins windows amd64 "%ROOT%backend\plugins-windows"
if errorlevel 1 exit /b 1
exit /b 0

:build_arm64
echo.
echo   Compiling Linux arm64...
set CGO_ENABLED=0
set GOOS=linux
set GOARCH=arm64
set GOARM=
go build -ldflags="-s -w" -o iot-gateway-linux-arm64 .
if errorlevel 1 (
    echo [FAIL] Linux arm64 build failed!
    exit /b 1
)
for /f %%i in ('powershell -Command "Get-ChildItem iot-gateway-linux-arm64 ^| ForEach-Object { [math]::Round($_.Length/1MB, 2) }"') do (
    echo [OK] Linux arm64 build successful! Size: %%i MB
)
call :build_plugins linux arm64 "%ROOT%backend\plugins-linux-arm64"
if errorlevel 1 exit /b 1
exit /b 0

:build_arm32
echo.
echo   Compiling Linux arm 32-bit (GOARM=7)...
set CGO_ENABLED=0
set GOOS=linux
set GOARCH=arm
set GOARM=7
go build -ldflags="-s -w" -o iot-gateway-linux-arm .
if errorlevel 1 (
    echo [FAIL] Linux arm build failed!
    exit /b 1
)
for /f %%i in ('powershell -Command "Get-ChildItem iot-gateway-linux-arm ^| ForEach-Object { [math]::Round($_.Length/1MB, 2) }"') do (
    echo [OK] Linux arm build successful! Size: %%i MB
)
call :build_plugins linux arm "%ROOT%backend\plugins-linux-arm" 7
if errorlevel 1 exit /b 1
exit /b 0

:: build_plugins <GOOS> <GOARCH> <OUTDIR> [GOARM]
:: Cross-compile every backend\driversrc\* plugin into OUTDIR as iot-driver-<name>.
::
:: Plugins are NOT optional: all protocol drivers live in plugins now, the main
:: binary has no built-in protocol left. An empty plugins dir means the gateway
:: supports nothing, so a failure here is fatal.
::
:: The layout is deliberately FLAT (driversrc\<name>\main.go); the output name is
:: the directory basename, so names must be unique and must not be nested.
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
pushd "%ROOT%backend"
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
            endlocal & popd & exit /b 1
        )
        echo     [OK] iot-driver-!PB_NAME!%PB_EXT%
        set /a PB_BUILT+=1
    )
)
if %PB_BUILT%==0 (
    echo [FAIL] no protocol plugin built -- the package must ship plugins\
    endlocal & popd & exit /b 1
)
endlocal & popd & exit /b 0
