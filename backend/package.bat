@echo off
:: 多平台打包脚本 - Build for Linux & Windows
:: ============================================

:: Linux 64-bit
:: ============
set CGO_ENABLED=0
set GOOS=linux
set GOARCH=amd64

echo.
echo ========================================
echo   Compiling Linux 64-bit...
echo ========================================
echo GOOS=%GOOS%  GOARCH=%GOARCH%  CGO_ENABLED=%CGO_ENABLED%

go build -ldflags="-s -w" -o ..\iot-gateway .

if errorlevel 1 (
    echo [FAIL] Linux build failed!
    exit /b 1
)

for /f %%i in ('powershell -Command "Get-ChildItem ..\iot-gateway ^| ForEach-Object { [math]::Round($_.Length/1MB, 2) }"') do (
    echo [OK] Linux build successful! Size: %%i MB
)

:: Windows 64-bit
:: ==============
set CGO_ENABLED=0
set GOOS=windows
set GOARCH=amd64

echo.
echo ========================================
echo   Compiling Windows 64-bit...
echo ========================================
echo GOOS=%GOOS%  GOARCH=%GOARCH%  CGO_ENABLED=%CGO_ENABLED%

go build -ldflags="-s -w" -o ..\iot-gateway.exe .

if errorlevel 1 (
    echo [FAIL] Windows build failed!
    exit /b 1
)

for /f %%i in ('powershell -Command "Get-ChildItem ..\iot-gateway.exe ^| ForEach-Object { [math]::Round($_.Length/1MB, 2) }"') do (
    echo [OK] Windows build successful! Size: %%i MB
)

:: Done
:: ====
echo.
echo ========================================
echo   All builds completed successfully!
echo ========================================
echo   - iot-gateway     (Linux)
echo   - iot-gateway.exe (Windows)
echo ========================================
