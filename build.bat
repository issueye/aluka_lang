@echo off
rem ============================================================
rem  Aluka local build script (Windows)
rem  Equivalent to: make build
rem  Output: bin\aluka.exe
rem  ============================================================
setlocal

set VERSION=0.2.0-dev
set BINARY=aluka
set PKG=./cmd/aluka

rem Switch to script directory (repo root) so it works from any cwd
cd /d "%~dp0"

where go >nul 2>nul
if errorlevel 1 (
    echo [ERROR] go not found. Install Go 1.25+ and add it to PATH
    exit /b 1
)

echo [1/2] Building %BINARY%.exe (version %VERSION%) ...
set CGO_ENABLED=0
go build -ldflags "-s -w -X main.version=%VERSION%" -o "bin\%BINARY%.exe" %PKG%
if errorlevel 1 (
    echo [ERROR] Build failed
    exit /b 1
)

echo [2/2] Done: %~dp0bin\%BINARY%.exe
exit /b 0
