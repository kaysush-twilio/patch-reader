#!/bin/bash
set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo -e "${GREEN}=== patch-reader installer ===${NC}"
echo

# Detect OS and architecture
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)

case "$ARCH" in
    x86_64)  ARCH="amd64" ;;
    aarch64) ARCH="arm64" ;;
    arm64)   ARCH="arm64" ;;
esac

BINARY_NAME="patch-reader-${OS}-${ARCH}"
if [[ "$OS" == "windows" ]]; then
    BINARY_NAME="${BINARY_NAME}.exe"
fi

echo "Detected: ${OS}/${ARCH}"
echo

# Check if we're in the repo or need to download
if [[ -f "main.go" ]]; then
    echo -e "${YELLOW}Building from source...${NC}"

    # Check for Go
    if ! command -v go &> /dev/null; then
        echo -e "${RED}Error: Go is not installed. Please install Go first.${NC}"
        echo "  brew install go  (macOS)"
        echo "  apt install golang  (Ubuntu/Debian)"
        exit 1
    fi

    # Build
    mkdir -p build
    go build -ldflags="-s -w" -o build/patch-reader .
    BINARY_PATH="build/patch-reader"
    echo -e "${GREEN}Build successful!${NC}"
else
    echo -e "${YELLOW}Downloading latest release...${NC}"

    # Get latest release URL
    RELEASE_URL="https://github.com/kaysush-twilio/patch-reader/releases/latest/download/${BINARY_NAME}"

    BINARY_PATH="/tmp/patch-reader"
    if ! curl -fsSL "$RELEASE_URL" -o "$BINARY_PATH"; then
        echo -e "${RED}Error: Failed to download binary${NC}"
        echo "URL: $RELEASE_URL"
        exit 1
    fi
    chmod +x "$BINARY_PATH"
    echo -e "${GREEN}Download successful!${NC}"
fi

echo

# Install location
INSTALL_DIR="/usr/local/bin"
INSTALL_PATH="${INSTALL_DIR}/patch-reader"

echo -e "${YELLOW}Installing to ${INSTALL_PATH}...${NC}"

if [[ -w "$INSTALL_DIR" ]]; then
    cp "$BINARY_PATH" "$INSTALL_PATH"
else
    echo "Requires sudo to install to ${INSTALL_DIR}"
    sudo cp "$BINARY_PATH" "$INSTALL_PATH"
fi

chmod +x "$INSTALL_PATH"
echo -e "${GREEN}Installed successfully!${NC}"
echo

# Verify installation
if command -v patch-reader &> /dev/null; then
    echo -e "${GREEN}✓ patch-reader is now available in your PATH${NC}"
else
    echo -e "${YELLOW}Note: You may need to restart your terminal or run: hash -r${NC}"
fi

echo

# Optional: Set up alias with AWS profile
echo -e "${YELLOW}Optional: Set up alias with AWS profile${NC}"
echo "This creates a 'pr' alias that includes your AWS profile."
echo

read -p "Would you like to set up the alias? (y/n) " -n 1 -r
echo

if [[ $REPLY =~ ^[Yy]$ ]]; then
    # Detect shell config file
    SHELL_NAME=$(basename "$SHELL")
    case "$SHELL_NAME" in
        zsh)  SHELL_RC="$HOME/.zshrc" ;;
        bash) SHELL_RC="$HOME/.bashrc" ;;
        *)    SHELL_RC="$HOME/.profile" ;;
    esac

    read -p "Enter AWS profile name [memora-dev-admin]: " AWS_PROFILE_NAME
    AWS_PROFILE_NAME=${AWS_PROFILE_NAME:-memora-dev-admin}

    # Check if alias already exists
    if grep -q "alias pr=" "$SHELL_RC" 2>/dev/null; then
        echo -e "${YELLOW}Alias 'pr' already exists in ${SHELL_RC}${NC}"
    else
        echo "" >> "$SHELL_RC"
        echo "# patch-reader alias" >> "$SHELL_RC"
        echo "alias pr='AWS_PROFILE=${AWS_PROFILE_NAME} patch-reader'" >> "$SHELL_RC"
        echo -e "${GREEN}✓ Added alias 'pr' to ${SHELL_RC}${NC}"
        echo "  Run: source ${SHELL_RC}"
    fi
fi

echo
echo -e "${GREEN}=== Installation complete ===${NC}"
echo
echo "Usage:"
echo "  patch-reader -profile-id <ID> -store-id <ID> [-patch-key <KEY>]"
echo
echo "Examples:"
echo "  # Interactive browser (all patches)"
echo "  patch-reader -profile-id mem_profile_01abc -store-id mem_store_01xyz -aws-profile memora-dev-admin"
echo
echo "  # With alias (if configured)"
echo "  pr -profile-id mem_profile_01abc -store-id mem_store_01xyz"
echo
echo "Run 'patch-reader -h' for all options."
