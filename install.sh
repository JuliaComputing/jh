#!/bin/bash

# JuliaHub CLI (jh) installer script
# This script downloads and installs the latest release of jh from GitHub

set -e

# Configuration
REPO_OWNER="JuliaComputing"
REPO_NAME="gojuliahub"
BINARY_NAME="jh"
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Logging functions
info() {
    echo -e "${BLUE}INFO${NC}: $1"
}

success() {
    echo -e "${GREEN}SUCCESS${NC}: $1"
}

warning() {
    echo -e "${YELLOW}WARNING${NC}: $1"
}

error() {
    echo -e "${RED}ERROR${NC}: $1" >&2
    exit 1
}

# Detect OS and architecture
detect_platform() {
    local os arch
    
    # Detect OS
    case "$(uname -s)" in
        Linux*)     os="linux";;
        Darwin*)    os="darwin";;
        CYGWIN*|MINGW*|MSYS*) os="windows";;
        *)          error "Unsupported operating system: $(uname -s)";;
    esac
    
    # Detect architecture
    case "$(uname -m)" in
        x86_64|amd64)   arch="amd64";;
        aarch64|arm64)  arch="arm64";;
        *)              error "Unsupported architecture: $(uname -m)";;
    esac
    
    echo "${os}-${arch}"
}

# Check if command exists
command_exists() {
    command -v "$1" >/dev/null 2>&1
}

# Get latest release version from GitHub API
get_latest_version() {
    local api_url="https://api.github.com/repos/${REPO_OWNER}/${REPO_NAME}/releases/latest"
    
    if command_exists curl; then
        curl -s "$api_url" | grep '"tag_name":' | sed -E 's/.*"tag_name": "([^"]+)".*/\1/'
    elif command_exists wget; then
        wget -qO- "$api_url" | grep '"tag_name":' | sed -E 's/.*"tag_name": "([^"]+)".*/\1/'
    else
        error "curl or wget is required to download the release"
    fi
}

# Download and install binary
install_binary() {
    local platform="$1"
    local version="$2"
    local os_part="${platform%-*}"
    local arch_part="${platform#*-}"
    
    # Construct download URL
    local binary_name="${BINARY_NAME}-${os_part}-${arch_part}"
    if [ "$os_part" = "windows" ]; then
        binary_name="${binary_name}.exe"
    fi
    
    local download_url="https://github.com/${REPO_OWNER}/${REPO_NAME}/releases/download/${version}/${binary_name}"
    
    info "Downloading ${BINARY_NAME} ${version} for ${platform}..."
    info "Download URL: $download_url"
    
    # Create install directory if it doesn't exist
    mkdir -p "$INSTALL_DIR"
    
    # Download binary
    local temp_file="${INSTALL_DIR}/${binary_name}.tmp"
    local final_file="${INSTALL_DIR}/${BINARY_NAME}"
    
    if [ "$os_part" = "windows" ]; then
        final_file="${final_file}.exe"
    fi
    
    if command_exists curl; then
        curl -L -o "$temp_file" "$download_url"
    elif command_exists wget; then
        wget -O "$temp_file" "$download_url"
    else
        error "curl or wget is required to download the release"
    fi
    
    # Verify download was successful
    if [ ! -f "$temp_file" ] || [ ! -s "$temp_file" ]; then
        rm -f "$temp_file"
        error "Failed to download binary from $download_url"
    fi
    
    # Move to final location and make executable
    mv "$temp_file" "$final_file"
    chmod +x "$final_file"
    
    success "Installed ${BINARY_NAME} to $final_file"
    
    # Check if install directory is in PATH
    case ":$PATH:" in
        *":$INSTALL_DIR:"*) ;;
        *) warning "$INSTALL_DIR is not in your PATH. Add it to your shell profile:"
           echo "  echo 'export PATH=\"$INSTALL_DIR:\$PATH\"' >> ~/.bashrc"
           echo "  echo 'export PATH=\"$INSTALL_DIR:\$PATH\"' >> ~/.zshrc"
           ;;
    esac
}

# Verify installation
verify_installation() {
    local binary_path="${INSTALL_DIR}/${BINARY_NAME}"
    if [ "$(uname -s)" = "CYGWIN" ] || [ "$(uname -s)" = "MINGW" ] || [ "$(uname -s)" = "MSYS" ]; then
        binary_path="${binary_path}.exe"
    fi
    
    if [ -x "$binary_path" ]; then
        local version_output
        if version_output=$("$binary_path" --version 2>/dev/null); then
            success "Installation verified: $version_output"
            info "Run '$BINARY_NAME --help' to get started"
        else
            warning "Binary installed but version check failed"
        fi
    else
        error "Installation failed: binary not found or not executable"
    fi
}

# Main installation function
main() {
    echo "JuliaHub CLI (${BINARY_NAME}) Installer"
    echo "======================================"
    
    # Detect platform
    local platform
    platform=$(detect_platform)
    info "Detected platform: $platform"
    
    # Get latest version
    info "Fetching latest release information..."
    local version
    version=$(get_latest_version)
    
    if [ -z "$version" ]; then
        error "Failed to get latest version information"
    fi
    
    info "Latest version: $version"
    
    # Check if binary already exists
    local existing_binary="${INSTALL_DIR}/${BINARY_NAME}"
    if [ "$(uname -s | tr '[:upper:]' '[:lower:]')" = "windows" ]; then
        existing_binary="${existing_binary}.exe"
    fi
    
    if [ -f "$existing_binary" ]; then
        if current_version=$("$existing_binary" --version 2>/dev/null); then
            info "Current installation: $current_version"
            if echo "$current_version" | grep -q "$version"; then
                info "Latest version is already installed"
                exit 0
            fi
        fi
        warning "Existing installation found. It will be replaced."
    fi
    
    # Install binary
    install_binary "$platform" "$version"
    
    # Verify installation
    verify_installation
    
    echo ""
    success "Installation complete!"
    info "You can now use '${BINARY_NAME}' to interact with JuliaHub"
    info "Start with: ${BINARY_NAME} auth login"
}

# Parse command line arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --install-dir)
            INSTALL_DIR="$2"
            shift 2
            ;;
        --help|-h)
            echo "JuliaHub CLI Installer"
            echo ""
            echo "Usage: $0 [options]"
            echo ""
            echo "Options:"
            echo "  --install-dir DIR   Install directory (default: \$HOME/.local/bin)"
            echo "  --help, -h          Show this help message"
            echo ""
            echo "Environment variables:"
            echo "  INSTALL_DIR         Same as --install-dir"
            exit 0
            ;;
        *)
            error "Unknown option: $1. Use --help for usage information."
            ;;
    esac
done

# Run main installation
main