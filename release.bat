@echo off
:: Keep this file pure ASCII, comments included. cmd.exe parses batch files with
:: the console code page; non-ASCII (GBK or UTF-8) lines can desync its line
:: offsets and make it run the tail of a comment line as a command -- and only
:: sometimes, which is far worse to debug.
:: ============================================
::  iot-gateway release packaging (Windows)
::  Frontend build -> Go cross-compile -> per-platform archives + SHA256
::
::  Usage:
::    release.bat           version taken from the latest git tag (e.g. v0.1.0),
::                          falls back to dev
::    release.bat v0.2.0    explicit version
::
::  Output (release\ directory):
::    iot-gateway-<version>-windows-amd64.zip
::    iot-gateway-<version>-linux-amd64.tar.gz
::    iot-gateway-<version>-linux-arm64.tar.gz
::    SHA256SUMS.txt
::
::  Feature parity with release.sh: same three platforms, same archive layout
::  (main binary + plugins\ + install/uninstall scripts + LICENSE). Change one
::  and change the other, or Windows and Linux releases start to differ.
::
::  Linux archives are built with GNU tar (--owner=0 zeroes the owner so an admin
::  extracting it does not end up with odd uids). Without GNU tar we fall back to
::  the bundled Windows bsdtar, whose --uid/--gid/--uname/--gname are the
::  libarchive equivalent. Do NOT switch to PowerShell Compress-Archive for the
::  Linux archives -- it mangles non-ASCII paths.
::  GNU tar forks a gzip child for -z, so it is only used when a gzip is reachable
::  (Git ships one next to tar, in <Git>\usr\bin -- which a plain cmd PATH does
::  not include; see :ensure_gzip). Otherwise bsdtar, which has zlib built in.
:: ============================================
setlocal
set "ROOT=%~dp0"
if "%ROOT:~-1%"=="\" set "ROOT=%ROOT:~0,-1%"
set "OUT=%ROOT%\release"
:: Call PowerShell by absolute path: it is always installed there, but it is not
:: guaranteed to be on PATH (a minimal/odd PATH made the checksum step fail with
:: "'powershell' is not recognized"). Fall back to the bare name just in case.
set "PS=%SystemRoot%\System32\WindowsPowerShell\v1.0\powershell.exe"
if not exist "%PS%" set "PS=powershell"
pushd "%ROOT%"

:: ---------- Version: argument first, otherwise the latest tag ----------
set "VERSION=%1"
if not "%VERSION%"=="" goto :version_ready
:: Inside a for /f command, cmd treats = as a delimiter, so --abbrev=0 must be
:: written --abbrev^=0. Otherwise git receives a lone "0" and dies with
:: fatal: Not a valid object name 0.
:: A goto is used instead of an if(...) block because a block swallows one level
:: of ^ during parsing, which would break the escape above.
:: No tag (no git, not a repository) falls back to dev.
for /f "usebackq delims=" %%v in (`git describe --tags --abbrev^=0 2^>nul`) do set "VERSION=%%v"
:version_ready
if "%VERSION%"=="" set "VERSION=dev"
echo ==^> Version: %VERSION%

:: ---------- Packaging tools ----------
:: TAR/TAR_OPTS: used for the Linux archives. ZIP_TAR: used for the Windows
:: archive (the bundled Windows tar.exe -a).
call :pick_tools
if not defined TAR (
    echo [FAIL] No tar found -- building Linux archives needs GNU tar or the bundled bsdtar
    goto :fail
)
if not defined ZIP_TAR (
    echo [FAIL] No zip tool found -- needs the bundled Windows tar.exe, Win10 1803+
    goto :fail
)

:: ---------- 1/4 Frontend build ----------
echo ==^> [1/4] Building frontend (npm run build) ...
pushd "%ROOT%\frontend"
if not exist node_modules (
    echo   node_modules missing, running npm install ...
    call npm install
    if errorlevel 1 (
        echo [FAIL] npm install failed
        popd & goto :fail
    )
)
call npm run build
if errorlevel 1 (
    echo [FAIL] Frontend build failed
    popd & goto :fail
)
popd

:: ---------- 2/4 Sync frontend output into the embed dir ----------
echo ==^> [2/4] Syncing frontend output to backend\web\dist ...
if exist "%ROOT%\backend\web\dist" rmdir /s /q "%ROOT%\backend\web\dist"
mkdir "%ROOT%\backend\web\dist"
xcopy /e /y /q "%ROOT%\frontend\dist\*" "%ROOT%\backend\web\dist\" >nul
if errorlevel 1 (
    echo [FAIL] Syncing frontend output failed
    goto :fail
)

:: ---------- 3/4 Cross-compile + package ----------
if exist "%OUT%" rmdir /s /q "%OUT%"
mkdir "%OUT%"

:: MANIFEST.json generator: build once on the host, reuse for every target.
:: It reads the SDK version from build info, drivers.version from disk and the
:: sha256 of every plugin, turning the release pairing matrix into a file that
:: ships with the package. Do not hand-build JSON in batch.
set "MANIFEST_TOOL=%OUT%\.manifest-tool.exe"
pushd "%ROOT%\backend"
go build -o "%MANIFEST_TOOL%" .\cmd\manifest
if errorlevel 1 (
    popd
    echo [FAIL] Building the MANIFEST.json generator failed
    goto :fail
)
popd

call :build_one windows amd64
if errorlevel 1 goto :fail
call :build_one linux amd64
if errorlevel 1 goto :fail
call :build_one linux arm64
if errorlevel 1 goto :fail

del /q "%MANIFEST_TOOL%" >nul 2>&1

:: ---------- 4/4 SHA256 checksums ----------
echo ==^> [4/4] Generating SHA256SUMS.txt ...
:: Line format matches sha256sum (hash + two spaces + ./name) and the file is
:: written with LF endings, so the two scripts produce interchangeable files.
:: We cd into release\ and use bare names so no non-ASCII absolute path is ever
:: handed to powershell.
pushd "%OUT%"
"%PS%" -NoProfile -Command "$f=@(Get-ChildItem *.zip)+@(Get-ChildItem *.tar.gz); $o=foreach($x in $f) { '{0}  ./{1}' -f (Get-FileHash $x.FullName -Algorithm SHA256).Hash.ToLower(), $x.Name }; [IO.File]::WriteAllText('SHA256SUMS.txt', (($o -join [char]10)+[char]10))"
if errorlevel 1 (
    popd
    echo [FAIL] Generating SHA256SUMS.txt failed
    goto :fail
)
popd

echo.
echo ============================================
echo   Release packaging complete: release\
echo ============================================
dir "%OUT%"
popd
:: Keep the window open when double-clicked, like install.bat. A redirected
:: stdin (CI, `cmd /c ... < nul`) makes pause return immediately.
pause
endlocal
exit /b 0

:fail
echo.
echo [FAIL] Release packaging failed, see the log above.
popd
pause
endlocal
exit /b 1

:: ============================================
:: Subroutines
:: ============================================

:: pick_tools -- choose the tar used for Linux archives and the tar.exe used for
:: zips. Results go into TAR / TAR_OPTS / ZIP_TAR (empty = not found).
:pick_tools
set "TAR="
set "TAR_OPTS="
set "TAR_DIR="
set "ZIP_TAR="
:: 1) a GNU tar on PATH wins
for /f "delims=" %%p in ('where tar 2^>nul') do if not defined TAR call :is_gnu_tar "%%~p"
:: 2) Git for Windows ships its own GNU tar; a plain cmd PATH usually only has
::    the System32 bsdtar, so derive the install root from git --exec-path
::    (...\Git\mingw64\libexec\git-core -> ...\Git)
if not defined TAR (
    for /f "usebackq delims=" %%g in (`git --exec-path 2^>nul`) do (
        if not defined TAR call :is_gnu_tar "%%~g\..\..\..\usr\bin\tar.exe"
    )
)
:: GNU tar forks out to gzip for -z, and Git's copy sits in <Git>\usr\bin, which
:: a plain cmd PATH normally does not have. Without this the packaging step dies
:: with "/bin/sh: line 1: gzip: command not found" and a broken pipe.
if defined TAR call :ensure_gzip
:: The GNU branch needs --force-local: GNU tar reads the drive colon in "d:\..."
:: as a remote "host:path" and dies with "Cannot connect to d: resolve failed".
:: release.sh never hits this because Git Bash hands it /d/... style paths.
:: (Never put :: comments inside a block -- cmd treats :: as a label and it can
:: break the whole block.)
if defined TAR (
    set "TAR_OPTS=--force-local --owner=0 --group=0 --numeric-owner"
    goto :pick_zip
)
:: 3) fall back to bsdtar: --uid/--gid/--uname/--gname are the libarchive
::    equivalent of GNU's --owner/--group/--numeric-owner, and bsdtar does not
::    parse the drive colon as a remote host, so it needs no --force-local.
for /f "delims=" %%p in ('where tar 2^>nul') do if not defined TAR set "TAR=%%~p"
if defined TAR set "TAR_OPTS=--uid 0 --gid 0 --uname root --gname root"

:pick_zip
:: zips go through the bundled Windows tar.exe (-a picks zip from the extension),
:: never through PowerShell.
if exist "%SystemRoot%\System32\tar.exe" set "ZIP_TAR=%SystemRoot%\System32\tar.exe"
exit /b 0

:: is_gnu_tar <tar path> -- record it (and its directory) in TAR / TAR_DIR if it
:: is GNU tar
:is_gnu_tar
if "%~1"=="" exit /b 0
if not exist "%~1" exit /b 0
"%~1" --version > "%TEMP%\iot-gateway-tarver.txt" 2>nul
findstr /i /c:"GNU tar" "%TEMP%\iot-gateway-tarver.txt" >nul 2>&1
if not errorlevel 1 (
    set "TAR=%~1"
    set "TAR_DIR=%~dp1"
)
del /q "%TEMP%\iot-gateway-tarver.txt" >nul 2>&1
exit /b 0

:: ensure_gzip -- make sure the gzip GNU tar shells out to is reachable.
:: First put tar's own directory on PATH (Git ships gzip.exe right next to tar),
:: then check. If there is no gzip anywhere, drop the GNU tar: the bsdtar
:: fallback links zlib in-process and needs no child at all.
:ensure_gzip
if exist "%TAR_DIR%gzip.exe" set "PATH=%TAR_DIR%;%PATH%"
where gzip >nul 2>&1
if errorlevel 1 (
    set "TAR="
    set "TAR_DIR="
)
exit /b 0

:: build_one <GOOS> <GOARCH>
:: Builds the main binary plus same-platform plugins into a scratch dir under
:: release\, packs them, then removes the intermediates.
:build_one
setlocal
set "BO_OS=%1"
set "BO_ARCH=%2"
set "BO_EXT="
if /i "%BO_OS%"=="windows" set "BO_EXT=.exe"

echo.
echo ==^> [3/4] Compiling %BO_OS%/%BO_ARCH% ...
pushd "%ROOT%\backend"
set CGO_ENABLED=0
set GOOS=%BO_OS%
set GOARCH=%BO_ARCH%
set GOARM=
go build -ldflags="-s -w -X main.version=%VERSION%" -o "%OUT%\iot-gateway%BO_EXT%" .
if errorlevel 1 (
    echo [FAIL] %BO_OS%/%BO_ARCH% main binary build failed
    popd & endlocal & exit /b 1
)
popd

:: Out-of-process protocol plugins, cross-compiled for the same platform and
:: packed into the plugins\ dir of the archive. A failure aborts the release --
:: plugins are a required part of the package; without them the gateway does
:: not support a single protocol.
call :build_plugins %BO_OS% %BO_ARCH% "%OUT%\plugins"
if errorlevel 1 (
    endlocal & exit /b 1
)

:: Package manifest: gateway/SDK/driver/wire versions plus every plugin sha256.
"%MANIFEST_TOOL%" -out "%OUT%\MANIFEST.json" -gateway "%VERSION%" -drivers-file "%ROOT%\drivers.version" -os "%BO_OS%" -arch "%BO_ARCH%" -plugins "%OUT%\plugins"
if errorlevel 1 (
    echo [FAIL] %BO_OS%/%BO_ARCH% manifest generation failed
    endlocal & exit /b 1
)

if /i "%BO_OS%"=="windows" (
    "%ZIP_TAR%" -a -cf "%OUT%\iot-gateway-%VERSION%-windows-%BO_ARCH%.zip" -C "%OUT%" iot-gateway.exe plugins MANIFEST.json -C "%ROOT%" install.bat uninstall.bat LICENSE
) else (
    "%TAR%" czf "%OUT%\iot-gateway-%VERSION%-%BO_OS%-%BO_ARCH%.tar.gz" %TAR_OPTS% -C "%OUT%" iot-gateway plugins MANIFEST.json -C "%ROOT%" install.sh uninstall.sh LICENSE
)
if errorlevel 1 (
    echo [FAIL] %BO_OS%/%BO_ARCH% packaging failed
    endlocal & exit /b 1
)

del /q "%OUT%\iot-gateway%BO_EXT%" >nul 2>&1
del /q "%OUT%\MANIFEST.json" >nul 2>&1
rmdir /s /q "%OUT%\plugins" >nul 2>&1
echo   [OK] %BO_OS%/%BO_ARCH% packaged
endlocal & exit /b 0

:: build_plugins <GOOS> <GOARCH> <OUTDIR>
:: Resolve every driver listed in drivers.txt at drivers.version from the module
:: proxy and cross-compile it into OUTDIR as iot-driver-<name> (same approach as
:: build.bat / package.bat).
::
:: Output names carry no version suffix; -X main.version stamps the version in.
:build_plugins
setlocal enabledelayedexpansion
set "PB_GOOS=%1"
set "PB_ARCH=%2"
set "PB_OUT=%~3"
set "PB_EXT="
if /i "%PB_GOOS%"=="windows" set "PB_EXT=.exe"
if not exist "%PB_OUT%" mkdir "%PB_OUT%"
set PB_BUILT=0
set "PB_VER="
:: for /f reads both line endings; drivers.txt is pinned to LF in .gitattributes
for /f "usebackq delims=" %%v in ("%ROOT%\drivers.version") do set "PB_VER=%%v"
if not defined PB_VER (
    echo [FAIL] "%ROOT%\drivers.version" is empty
    endlocal & exit /b 1
)
set "PB_TMP=%TEMP%\iot-driver-build"
if exist "%PB_TMP%" rmdir /s /q "%PB_TMP%"
mkdir "%PB_TMP%"
pushd "%PB_TMP%"
set GOWORK=off
go mod init driverbuild >nul 2>&1
for /f "usebackq delims=" %%n in ("%ROOT%\drivers.txt") do (
    set CGO_ENABLED=0
    set GOOS=%PB_GOOS%
    set GOARCH=%PB_ARCH%
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
    rem Keep echo text in this block free of parentheses: cmd reads ) as the end
    rem of the block.
    echo     [plugin] %%n !PB_VER! %PB_GOOS%/%PB_ARCH%
    set /a PB_BUILT+=1
)
popd
rmdir /s /q "%PB_TMP%"
if %PB_BUILT%==0 (
    echo [FAIL] no protocol plugin built -- the package must ship plugins\
    endlocal & exit /b 1
)
echo   [OK] %PB_GOOS%/%PB_ARCH%: %PB_BUILT% protocol plugins
endlocal & exit /b 0
