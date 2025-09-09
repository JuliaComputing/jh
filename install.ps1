#!/usr/bin/env pwsh

# JuliaHub CLI (jh) installer script for Windows PowerShell
# This script downloads and installs the latest release of jh from GitHub

param(
    [string]$InstallDir = "$env:USERPROFILE\.local\bin",
    [switch]$Help
)

# Configuration
$RepoOwner = "JuliaComputing"
$RepoName = "jh" 
$BinaryName = "jh"

# Colors for output
$Colors = @{
    Red = "Red"
    Green = "Green" 
    Yellow = "Yellow"
    Blue = "Cyan"
}

# Logging functions
function Write-Info {
    param([string]$Message)
    Write-Host "INFO: $Message" -ForegroundColor $Colors.Blue
}

function Write-Success {
    param([string]$Message)
    Write-Host "SUCCESS: $Message" -ForegroundColor $Colors.Green
}

function Write-Warning {
    param([string]$Message)
    Write-Host "WARNING: $Message" -ForegroundColor $Colors.Yellow
}

function Write-Error {
    param([string]$Message)
    Write-Host "ERROR: $Message" -ForegroundColor $Colors.Red
    exit 1
}

# Show help
if ($Help) {
    Write-Host "JuliaHub CLI Installer for PowerShell"
    Write-Host ""
    Write-Host "Usage: .\install.ps1 [options]"
    Write-Host ""
    Write-Host "Parameters:"
    Write-Host "  -InstallDir DIR     Install directory (default: `$env:USERPROFILE\.local\bin)"
    Write-Host "  -Help               Show this help message"
    Write-Host ""
    Write-Host "Environment variables:"
    Write-Host "  INSTALL_DIR         Same as -InstallDir"
    exit 0
}

# Override with environment variable if set
if ($env:INSTALL_DIR) {
    $InstallDir = $env:INSTALL_DIR
}

Write-Host "JuliaHub CLI ($BinaryName) Installer for PowerShell"
Write-Host "=================================================="

# Detect architecture
$Arch = "amd64"
if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64") {
    $Arch = "arm64"
}

Write-Info "Detected platform: windows-$Arch"

# Get latest version from GitHub API
Write-Info "Fetching latest release information..."
try {
    $ApiUrl = "https://api.github.com/repos/$RepoOwner/$RepoName/releases/latest"
    $Response = Invoke-RestMethod -Uri $ApiUrl -Method Get
    $Version = $Response.tag_name
    
    if (-not $Version) {
        Write-Error "Failed to get latest version information"
    }
    
    Write-Info "Latest version: $Version"
}
catch {
    Write-Error "Failed to fetch release information: $($_.Exception.Message)"
}

# Construct download URL and filenames
$BinaryFile = "$BinaryName-windows-$Arch.exe"
$DownloadUrl = "https://github.com/$RepoOwner/$RepoName/releases/download/$Version/$BinaryFile"
$TempFile = Join-Path $InstallDir "$BinaryFile.tmp"
$FinalFile = Join-Path $InstallDir "$BinaryName.exe"

# Create install directory if it doesn't exist
if (-not (Test-Path $InstallDir)) {
    New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
}

# Check if binary already exists
if (Test-Path $FinalFile) {
    Write-Info "Checking current installation..."
    try {
        $CurrentVersion = & $FinalFile --version 2>$null
        if ($CurrentVersion -and $CurrentVersion -match $Version) {
            Write-Info "Current installation: $CurrentVersion"
            Write-Info "Latest version is already installed"
            exit 0
        }
        if ($CurrentVersion) {
            Write-Info "Current installation: $CurrentVersion"
        }
    }
    catch {
        # Ignore version check errors
    }
    Write-Warning "Existing installation found. It will be replaced."
}

Write-Info "Downloading $BinaryName $Version for windows-$Arch..."
Write-Info "Download URL: $DownloadUrl"

# Download binary
try {
    Invoke-WebRequest -Uri $DownloadUrl -OutFile $TempFile -UseBasicParsing
    
    # Verify download was successful
    if (-not (Test-Path $TempFile) -or (Get-Item $TempFile).Length -eq 0) {
        Remove-Item $TempFile -ErrorAction SilentlyContinue
        Write-Error "Failed to download binary from $DownloadUrl"
    }
    
    # Move to final location
    Move-Item $TempFile $FinalFile -Force
    
    Write-Success "Installed $BinaryName to $FinalFile"
}
catch {
    Remove-Item $TempFile -ErrorAction SilentlyContinue
    Write-Error "Failed to download or install binary: $($_.Exception.Message)"
}

# Check if install directory is in PATH
$PathDirs = $env:PATH -split ";"
if ($InstallDir -notin $PathDirs) {
    Write-Warning "$InstallDir is not in your PATH."
    Write-Host "To add it permanently for current user, run:"
    Write-Host "  `$env:PATH += ';$InstallDir'; [Environment]::SetEnvironmentVariable('PATH', `$env:PATH, 'User')" -ForegroundColor Yellow
    Write-Host "Or add it to your current session:"
    Write-Host "  `$env:PATH += ';$InstallDir'" -ForegroundColor Yellow
    Write-Host ""
}

# Verify installation
if (Test-Path $FinalFile) {
    Write-Info "Verifying installation..."
    try {
        $VersionOutput = & $FinalFile --version 2>$null
        if ($VersionOutput) {
            Write-Success "Installation verified: $VersionOutput"
            Write-Info "Run '$BinaryName --help' to get started"
        } else {
            Write-Warning "Binary installed but version check failed"
        }
    }
    catch {
        Write-Warning "Binary installed but version check failed"
    }
} else {
    Write-Error "Installation failed: binary not found"
}

Write-Host ""
Write-Success "Installation complete!"
Write-Info "You can now use '$BinaryName' to interact with JuliaHub"
Write-Info "Start with: $BinaryName auth login"
