@echo off
setlocal enabledelayedexpansion

:: JuliaHub CLI (jh) installer script for Windows
:: This script downloads and installs the latest release of jh from GitHub

:: Configuration
set REPO_OWNER=JuliaComputing
set REPO_NAME=jh
set BINARY_NAME=jh
if "%INSTALL_DIR%"=="" set INSTALL_DIR=%USERPROFILE%\.local\bin

:: Create install directory if it doesn't exist
if not exist "%INSTALL_DIR%" mkdir "%INSTALL_DIR%"

echo JuliaHub CLI (%BINARY_NAME%) Installer for Windows
echo ================================================

:: Check if curl is available (Windows 10 1803+ has curl built-in)
curl --version >nul 2>&1
if %errorlevel% neq 0 (
    echo ERROR: curl is required but not found. Please install curl or use PowerShell.
    echo You can install curl from: https://curl.se/download.html
    echo Or use the PowerShell install script instead.
    exit /b 1
)

echo INFO: Fetching latest release information...

:: Get latest version from GitHub API
for /f "tokens=*" %%i in ('curl -s "https://api.github.com/repos/%REPO_OWNER%/%REPO_NAME%/releases/latest" ^| findstr "tag_name" ^| for /f "tokens=2 delims=:," %%j in ('findstr "tag_name"') do @echo %%~j') do set VERSION=%%i
set VERSION=%VERSION:"=%
set VERSION=%VERSION: =%

if "%VERSION%"=="" (
    echo ERROR: Failed to get latest version information
    exit /b 1
)

echo INFO: Latest version: %VERSION%

:: Detect architecture
set ARCH=amd64
if "%PROCESSOR_ARCHITECTURE%"=="ARM64" set ARCH=arm64

:: Construct download URL and filenames
set BINARY_FILE=%BINARY_NAME%-windows-%ARCH%.exe
set DOWNLOAD_URL=https://github.com/%REPO_OWNER%/%REPO_NAME%/releases/download/%VERSION%/%BINARY_FILE%
set TEMP_FILE=%INSTALL_DIR%\%BINARY_FILE%.tmp
set FINAL_FILE=%INSTALL_DIR%\%BINARY_NAME%.exe

echo INFO: Downloading %BINARY_NAME% %VERSION% for windows-%ARCH%...
echo INFO: Download URL: %DOWNLOAD_URL%

:: Check if binary already exists
if exist "%FINAL_FILE%" (
    echo INFO: Checking current installation...
    for /f "tokens=*" %%i in ('"%FINAL_FILE%" --version 2^>nul') do set CURRENT_VERSION=%%i
    if not "%CURRENT_VERSION%"=="" (
        echo INFO: Current installation: !CURRENT_VERSION!
        echo !CURRENT_VERSION! | findstr "%VERSION%" >nul
        if !errorlevel! equ 0 (
            echo INFO: Latest version is already installed
            exit /b 0
        )
    )
    echo WARNING: Existing installation found. It will be replaced.
)

:: Download binary
curl -L -o "%TEMP_FILE%" "%DOWNLOAD_URL%"
if %errorlevel% neq 0 (
    echo ERROR: Failed to download binary from %DOWNLOAD_URL%
    if exist "%TEMP_FILE%" del "%TEMP_FILE%"
    exit /b 1
)

:: Verify download was successful
if not exist "%TEMP_FILE%" (
    echo ERROR: Downloaded file not found
    exit /b 1
)

:: Move to final location
move "%TEMP_FILE%" "%FINAL_FILE%" >nul
if %errorlevel% neq 0 (
    echo ERROR: Failed to install binary to %FINAL_FILE%
    exit /b 1
)

echo SUCCESS: Installed %BINARY_NAME% to %FINAL_FILE%

:: Check if install directory is in PATH
echo %PATH% | findstr /C:"%INSTALL_DIR%" >nul
if %errorlevel% neq 0 (
    echo WARNING: %INSTALL_DIR% is not in your PATH.
    echo To add it permanently, run:
    echo   setx PATH "%%PATH%%;%INSTALL_DIR%"
    echo Or add it to your current session:
    echo   set PATH=%%PATH%%;%INSTALL_DIR%
    echo.
)

:: Verify installation
if exist "%FINAL_FILE%" (
    echo INFO: Verifying installation...
    for /f "tokens=*" %%i in ('"%FINAL_FILE%" --version 2^>nul') do set VERSION_OUTPUT=%%i
    if not "!VERSION_OUTPUT!"=="" (
        echo SUCCESS: Installation verified: !VERSION_OUTPUT!
        echo INFO: Run '%BINARY_NAME% --help' to get started
    ) else (
        echo WARNING: Binary installed but version check failed
    )
) else (
    echo ERROR: Installation failed: binary not found
    exit /b 1
)

echo.
echo SUCCESS: Installation complete!
echo INFO: You can now use '%BINARY_NAME%' to interact with JuliaHub
echo INFO: Start with: %BINARY_NAME% auth login

endlocal
