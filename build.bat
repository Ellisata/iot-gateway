@echo off
:: ============================================
::  iot-gateway ����ģʽһ������
::  ǰ�˹��� -> ͬ���� backend/web/dist -> Go ����
::  �÷�: build.bat          ���� Linux amd64 + arm64 + arm32 + Windows amd64�������ã�Ĭ�ϣ�
::        build.bat windows  ������ Windows amd64������ԭ������죩
::        build.bat arm64    ������ Linux arm64��ARM64 ���ػ� / ��ݮ�ɵȣ�
::        build.bat arm32    ������ Linux arm 32 λ��ARMv7 ���ذ壬�̶� GOARM=7��
::  ����: Ĭ��ģʽ�� backend\package.bat���������ڲֿ��Ŀ¼:
::          iot-gateway / iot-gateway-linux-arm64 / iot-gateway-linux-arm / iot-gateway.exe
::          plugins-linux\ / plugins-linux-arm64\ / plugins-linux-arm\ / plugins-windows\
::        ��ƽ̨ģʽ������ backend\ �£����ھ͵ص���
::
::  ע��: ���Ŀ¼һ�ɴ�ƽ̨��׺��Windows �� plugins-windows\������ plugin.dir
::        Ĭ���� "plugins"����� exe ����������ֱ���� exe ֮ǰҪ�Ѷ�ӦĿ¼������
::        plugins\ ��� plugin.dir������һ��Э��Ҳɨ���������а�����Ӱ�졣
::
::  arm32 �̶� GOARM=7��ARMv7-A + VFPv3 Ӳ���㣩��Ŀ������� ARMv6/ARMv5
::  ����ݮ�� 1 / Zero һ�����ø��� GOARM=5 �����㣬��������ڰ�����ֱ��������
:: ============================================
setlocal
set ROOT=%~dp0

:: all��Ĭ�ϣ�ȫ���� / windows / arm64 / arm32
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
rem arm �� Go �Ĺ���ָ 32 λ��GOARCH=arm����64 λ�� arm64��
if /i "%1"=="arm" (
    set BUILD_ALL=0
    set BUILD_ARM=0
    set BUILD_ARM32=1
)

echo.
echo [1/3] ����ǰ�� (npm run build) ...
pushd "%ROOT%frontend"
call npm run build
if errorlevel 1 goto :fail

echo [2/3] ͬ���������ﵽ backend\web\dist ...
if exist "%ROOT%backend\web\dist" (
    rmdir /s /q "%ROOT%backend\web\dist"
)
mkdir "%ROOT%backend\web\dist"
xcopy /e /y /q "%ROOT%frontend\dist\*" "%ROOT%backend\web\dist\" >nul
if errorlevel 1 goto :fail

echo [3/3] ���� Go ��� ...
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
    rem ȫ�����뽻�� package.bat���ܹ��嵥���� glob ֻ����һ�����������Ư�ơ�
    call "%ROOT%backend\package.bat"
    if errorlevel 1 goto :fail
)

echo.
echo ============================================
if "%BUILD_ALL%"=="0" (
  if "%BUILD_ARM%"=="1" (
    echo   �������:
    echo   - Linux arm64 : %ROOT%backend\iot-gateway-linux-arm64
    echo   - ���Ŀ¼    : %ROOT%backend\plugins-linux-arm64
  ) else if "%BUILD_ARM32%"=="1" (
    echo   �������:
    echo   - Linux arm32 : %ROOT%backend\iot-gateway-linux-arm
    echo   - ���Ŀ¼    : %ROOT%backend\plugins-linux-arm
  ) else (
    echo   �������:
    echo   - Windows     : %ROOT%backend\iot-gateway.exe
    echo   - ���Ŀ¼    : %ROOT%backend\plugins-windows
  )
) else (
  echo   ������ɣ������ڲֿ��Ŀ¼��:
  echo   - Linux amd64  : %ROOT%iot-gateway
  echo   - Linux arm64  : %ROOT%iot-gateway-linux-arm64
  echo   - Linux arm32  : %ROOT%iot-gateway-linux-arm
  echo   - Windows amd64: %ROOT%iot-gateway.exe
)
echo   ���з�ʽ: ˫�� run.bat ��ֱ������ iot-gateway.exe
echo   ���ʵ�ַ: http://localhost:9082/admin
echo ============================================
endlocal
exit /b 0

:fail
echo.
echo [FAIL] ����ʧ�ܣ���鿴�Ϸ���־��
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
pushd "%ROOT%backend\driversrc"
for /d %%d in (*) do (
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
