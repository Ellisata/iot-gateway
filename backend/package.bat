@echo off
:: Keep this file pure ASCII, comments included. cmd.exe parses batch files with
:: the console code page; non-ASCII (GBK or UTF-8) lines can desync its line
:: offsets and make it run the tail of a comment line as a command -- and only
:: sometimes, which is far worse to debug.
:: Multi-platform packaging: Linux amd64 / Linux arm64 / Linux arm(32) / Windows amd64
:: ============================================
:: Switch to the script's own directory first, so invoking it from anywhere works
:: (%~dp0 instead of the caller's cwd).
:: pushd rather than cd /d: over a UNC path (network share) cd /d fails outright.
::
:: Usage:
::   package.bat           all: Linux amd64 + Windows amd64 + Linux arm64 + Linux arm32
::   package.bat linux     Linux amd64 only
::   package.bat windows   Windows amd64 only
::   package.bat arm64     Linux arm64 only
::   package.bat arm32     Linux arm 32-bit only (GOARM=7 pinned)
::
:: Artifacts land in the repo root (the parent of this script):
::   iot-gateway              + plugins-linux\        Linux amd64
::   iot-gateway-linux-arm64  + plugins-linux-arm64\  Linux arm64
::   iot-gateway-linux-arm    + plugins-linux-arm\    Linux arm32 (GOARM=7)
::   iot-gateway.exe          + plugins-windows\      Windows amd64
::
::  NOTE: plugin dirs always carry a platform suffix (Windows is plugins-windows\).
::        The gateway resolves plugin.dir (default "plugins") relative to the exe,
::        so rename the matching dir to plugins\ (or point plugin.dir at it)
::        before running the exe locally, otherwise no protocol is found at all.
::        Release packages are unaffected.
::
:: arm32 pins GOARM=7 (ARMv7-A + VFPv3 hard float). If the target board is
:: ARMv6/ARMv5 (Raspberry Pi 1 / Zero gen 1) rebuild with GOARM=5 (soft float),
:: otherwise the binary will not even start there.
pushd "%~dp0"

:: Build version: inherited from build.bat when present, otherwise explicit
:: VERSION, the latest git tag, then dev. Injected as -X main.version so the
:: binary describes itself; without it the gateway identity falls back to
:: gateway.version in default.yaml.
if not "%BUILD_VERSION%"=="" goto :build_version_ready
set "BUILD_VERSION=%VERSION%"
if not "%BUILD_VERSION%"=="" goto :build_version_ready
for /f "usebackq delims=" %%v in (`git describe --tags --abbrev^=0 2^>nul`) do set "BUILD_VERSION=%%v"
:build_version_ready
if "%BUILD_VERSION%"=="" set "BUILD_VERSION=dev"

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
rem "arm" means 32-bit by Go convention (GOARCH=arm); 64-bit is arm64.
if /i "%PKG_TARGET%"=="arm" set DO_ARM32=1

if "%DO_LINUX%%DO_WINDOWS%%DO_ARM64%%DO_ARM32%"=="0000" (
    echo [FAIL] Unknown target: %PKG_TARGET%   usage: all / linux / windows / arm64 / arm32
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
:: The main flow stops here. Without this exit, execution falls through into the
:: :build_one / :build_plugins labels below, rebuilds the plugins with empty
:: %~1/%~2, and -- with %PB_OUT% empty -- writes them to the drive root.
popd
exit /b 0

:fail
echo.
echo [FAIL] Build failed, see the log above.
popd
exit /b 1


:: build_one <GOOS> <GOARCH> <main binary name> <plugin dir name> [GOARM]
:: The main binary and its plugins-* dir both land in the repo root (parent of
:: this script).
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

go build -ldflags="-s -w -X main.version=%BUILD_VERSION%" -o "..\%BO_BIN%" .
if errorlevel 1 (
    echo [FAIL] %BO_GOOS%/%BO_TAG% build failed!
    endlocal & exit /b 1
)

:: Out-of-process protocol plugins: cross-compiled for the same platform and the
:: SAME GOARM as the main binary, into the plugins-* dir of the same name.
:: A failure aborts the build -- see the notes at :build_plugins.
call :build_plugins %BO_GOOS% %BO_ARCH% "%~dp0..\%BO_PLUGINS%" %BO_GOARM%
if errorlevel 1 (
    endlocal & exit /b 1
)

for /f %%i in ('powershell -Command "Get-ChildItem ..\%BO_BIN% ^| ForEach-Object { [math]::Round($_.Length/1MB, 2) }"') do (
    echo [OK] %BO_BIN% built successfully! Size: %%i MB
)
endlocal & exit /b 0


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
for /f "usebackq delims=" %%v in ("%~dp0..\drivers.version") do set "PB_VER=%%v"
if not defined PB_VER (
    echo [FAIL] "%~dp0..\drivers.version" is empty
    endlocal & exit /b 1
)
set "PB_TMP=%TEMP%\iot-driver-build"
if exist "%PB_TMP%" rmdir /s /q "%PB_TMP%"
mkdir "%PB_TMP%"
pushd "%PB_TMP%"
set GOWORK=off
go mod init driverbuild >nul 2>&1
for /f "usebackq delims=" %%n in ("%~dp0..\drivers.txt") do (
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
