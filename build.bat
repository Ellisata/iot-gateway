@echo off
:: ============================================
::  iot-gateway 生产模式一键构建
::  前端构建 -> 同步到 backend/web/dist -> Go 编译
::  用法: build.bat          编译 Linux + Windows（部署用，默认）
::        build.bat windows  仅编译 Windows（本地运行快）
::  产物: backend\iot-gateway(.exe)
:: ============================================
setlocal
set ROOT=%~dp0

set BUILD_ALL=1
if /i "%1"=="windows" set BUILD_ALL=0
if /i "%1"=="win"     set BUILD_ALL=0

echo.
echo [1/3] 构建前端 (npm run build) ...
cd /d "%ROOT%frontend"
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
cd /d "%ROOT%backend"

if "%BUILD_ALL%"=="0" (
    call :build_windows
    if errorlevel 1 goto :fail
) else (
    call "%ROOT%backend\package.bat"
    if errorlevel 1 goto :fail
)

echo.
echo ============================================
if "%BUILD_ALL%"=="0" (
  echo   构建完成:
  echo   - Windows: %ROOT%backend\iot-gateway.exe
) else (
  echo   构建完成:
  echo   - Linux  : %ROOT%backend\iot-gateway
  echo   - Windows: %ROOT%backend\iot-gateway.exe
)
echo   运行方式: 双击 run.bat 或直接运行 iot-gateway.exe
echo   访问地址: http://localhost:9081/admin
echo ============================================
endlocal
exit /b 0

:build_windows
echo.
echo   Compiling Windows 64-bit...
set CGO_ENABLED=0
set GOOS=windows
set GOARCH=amd64
go build -ldflags="-s -w" -o iot-gateway.exe .
if errorlevel 1 (
    echo [FAIL] Windows build failed!
    exit /b 1
)
for /f %%i in ('powershell -Command "Get-ChildItem iot-gateway.exe ^| ForEach-Object { [math]::Round($_.Length/1MB, 2) }"') do (
    echo [OK] Windows build successful! Size: %%i MB
)
exit /b 0

:fail
echo.
echo [FAIL] 构建失败，请查看上方日志。
endlocal
exit /b 1
