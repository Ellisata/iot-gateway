@echo off
:: Keep this file pure ASCII, comments included. cmd.exe parses batch files with
:: the console code page; non-ASCII (GBK or UTF-8) lines can desync its line
:: offsets and make it run the tail of a comment line as a command -- and only
:: sometimes, which is far worse to debug.
:: ============================================
::  iot-gateway one-shot production build
::  Frontend build -> sync to backend/web/dist -> compile Go
::  Usage: build.bat          build Linux amd64 + arm64 + arm32 + Windows amd64 (default)
::         build.bat windows  Windows amd64 only (native, fastest)
::         build.bat arm64    Linux arm64 only (ARM64 IPC / Raspberry Pi)
::         build.bat arm32    Linux arm 32-bit only (ARMv7 board, GOARM=7 pinned)
::  Output: the default mode delegates to backend\package.bat; artifacts land in
::          the repo root:
::          iot-gateway / iot-gateway-linux-arm64 / iot-gateway-linux-arm / iot-gateway.exe
::          plugins-linux\ / plugins-linux-arm64\ / plugins-linux-arm\ / plugins-windows\
::          Single-platform mode writes into backend\ so you can run it in place.
::
::  NOTE: plugin dirs always carry a platform suffix (Windows is plugins-windows\).
::        The gateway resolves plugin.dir (default "plugins") relative to the exe,
::        so rename the matching dir to plugins\ (or point plugin.dir at it)
::        before running the exe locally, otherwise no protocol is found at all.
::        Release packages are unaffected.
::
::  arm32 pins GOARM=7 (ARMv7-A + VFPv3 hard float). If the target board is
::  ARMv6/ARMv5 (Raspberry Pi 1 / Zero gen 1) rebuild with GOARM=5 (soft float),
::  otherwise the binary will not even start there.
:: ============================================
setlocal
set ROOT=%~dp0

:: Build version: explicit VERSION wins, otherwise the latest git tag, then dev.
:: Injected as -X main.version so the binary describes itself; without it the
:: gateway identity falls back to gateway.version in default.yaml.
set "BUILD_VERSION=%VERSION%"
if not "%BUILD_VERSION%"=="" goto :build_version_ready
for /f "usebackq delims=" %%v in (`git -C "%ROOT%." describe --tags --abbrev^=0 2^>nul`) do set "BUILD_VERSION=%%v"
:build_version_ready
if "%BUILD_VERSION%"=="" set "BUILD_VERSION=dev"

:: all (default, everything) / windows / arm64 / arm32
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
rem "arm" means 32-bit by Go convention (GOARCH=arm); 64-bit is arm64.
if /i "%1"=="arm" (
    set BUILD_ALL=0
    set BUILD_ARM=0
    set BUILD_ARM32=1
)

echo.
echo [1/3] Building frontend (npm run build) ...
pushd "%ROOT%frontend"
call npm run build
if errorlevel 1 goto :fail

echo [2/3] Syncing build output to backend\web\dist ...
if exist "%ROOT%backend\web\dist" (
    rmdir /s /q "%ROOT%backend\web\dist"
)
mkdir "%ROOT%backend\web\dist"
xcopy /e /y /q "%ROOT%frontend\dist\*" "%ROOT%backend\web\dist\" >nul
if errorlevel 1 goto :fail

echo [3/3] Compiling Go backend ...
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
    rem Full builds go through package.bat: the platform list and plugin glob
    rem live in exactly one place, so the two scripts cannot drift apart.
    call "%ROOT%backend\package.bat"
    if errorlevel 1 goto :fail
)

echo.
echo ============================================
if "%BUILD_ALL%"=="0" (
  if "%BUILD_ARM%"=="1" (
    echo   Build complete:
    echo   - Linux arm64 : %ROOT%backend\iot-gateway-linux-arm64
    echo   - Plugins     : %ROOT%backend\plugins-linux-arm64
  ) else if "%BUILD_ARM32%"=="1" (
    echo   Build complete:
    echo   - Linux arm32 : %ROOT%backend\iot-gateway-linux-arm
    echo   - Plugins     : %ROOT%backend\plugins-linux-arm
  ) else (
    echo   Build complete:
    echo   - Windows     : %ROOT%backend\iot-gateway.exe
    echo   - Plugins     : %ROOT%backend\plugins-windows
  )
) else (
  echo   Build complete - artifacts are in the repo root:
  echo   - Linux amd64  : %ROOT%iot-gateway
  echo   - Linux arm64  : %ROOT%iot-gateway-linux-arm64
  echo   - Linux arm32  : %ROOT%iot-gateway-linux-arm
  echo   - Windows amd64: %ROOT%iot-gateway.exe
)
echo   Run: double-click run.bat or start iot-gateway.exe directly
echo   Console: http://localhost:9082/admin
echo ============================================
:: Keep the window open when double-clicked, like install.bat. A redirected
:: stdin (CI, `cmd /c ... < nul`) makes pause return immediately.
:: NOTE: the full build delegates to backend\package.bat -- when this script is
:: driven by another script, call it as `call build.bat ... < nul` or the pause
:: will wait for a keypress.
pause
endlocal
exit /b 0

:fail
echo.
echo [FAIL] Build failed, see the log above.
pause
endlocal
exit /b 1

:build_windows
echo.
echo   Compiling Windows 64-bit...
set CGO_ENABLED=0
set GOOS=windows
set GOARCH=amd64
set GOARM=
go build -ldflags="-s -w -X main.version=%BUILD_VERSION%" -o iot-gateway.exe .
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
go build -ldflags="-s -w -X main.version=%BUILD_VERSION%" -o iot-gateway-linux-arm64 .
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
go build -ldflags="-s -w -X main.version=%BUILD_VERSION%" -o iot-gateway-linux-arm .
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
:: Resolve every driver listed in drivers.txt at drivers.version from the module
:: proxy and cross-compile it into OUTDIR as iot-driver-<name>.
::
:: A throwaway module is used on purpose: go install refuses to cross-compile
:: when GOBIN is set, while go build needs a main module to resolve go.sum.
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
set "PB_VER="
for /f "usebackq delims=" %%v in ("%ROOT%drivers.version") do set "PB_VER=%%v"
if not defined PB_VER (
    echo [FAIL] "%ROOT%drivers.version" is empty
    endlocal & exit /b 1
)
set "PB_TMP=%TEMP%\iot-driver-build"
if exist "%PB_TMP%" rmdir /s /q "%PB_TMP%"
mkdir "%PB_TMP%"
pushd "%PB_TMP%"
set GOWORK=off
go mod init driverbuild >nul 2>&1
for /f "usebackq delims=" %%n in ("%ROOT%drivers.txt") do (
    set CGO_ENABLED=0
    set GOOS=%PB_GOOS%
    set GOARCH=%PB_ARCH%
    set GOARM=%PB_GOARM%
    go get "github.com/Ellisata/iot-gateway-drivers/%%n@!PB_VER!"
    if errorlevel 1 (
        echo [FAIL] plugin %%n resolve failed for %PB_GOOS%/%PB_ARCH%
        popd & rmdir /s /q "%PB_TMP%" & endlocal & exit /b 1
    )
    go build -ldflags="-s -w -X main.version=!PB_VER!" -o "%PB_OUT%\iot-driver-%%n%PB_EXT%" "github.com/Ellisata/iot-gateway-drivers/%%n"
    if errorlevel 1 (
        echo [FAIL] plugin %%n build failed for %PB_GOOS%/%PB_ARCH%
        popd & rmdir /s /q "%PB_TMP%" & endlocal & exit /b 1
    )
    echo     [OK] iot-driver-%%n%PB_EXT%
    set /a PB_BUILT+=1
)
popd
rmdir /s /q "%PB_TMP%"
if %PB_BUILT%==0 (
    echo [FAIL] no protocol plugin built -- the package must ship plugins\
    endlocal & exit /b 1
)
endlocal & exit /b 0
