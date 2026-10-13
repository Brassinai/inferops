#!/bin/sh
set -e

# InferOps CLI installer
# Usage: curl -fsSL https://get.inferops.dev | sh

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

INSTALL_PREFIX="${INSTALL_PREFIX:-}"
if [ -z "$INSTALL_PREFIX" ]; then
  if [ -w "/usr/local" ]; then
    INSTALL_PREFIX="/usr/local"
  else
    INSTALL_PREFIX="$HOME/.local"
  fi
fi

echo "Installing InferOps CLI..."

# Require python3
if ! command -v python3 >/dev/null 2>&1; then
  echo "${RED}Error: python3 is required but not installed.${NC}"
  echo "Install Python 3.10+ and try again."
  exit 1
fi

if ! python3 -c "import sys; sys.exit(0 if sys.version_info >= (3, 10) else 1)"; then
  PYTHON_VERSION=$(python3 -c 'import sys; print(f"{sys.version_info.major}.{sys.version_info.minor}")')
  echo "${RED}Error: Python 3.10+ is required. Found ${PYTHON_VERSION}.${NC}"
  exit 1
fi

# Ensure pip is available
if ! command -v pip3 >/dev/null 2>&1; then
  echo "${YELLOW}pip3 not found. Bootstrapping pip...${NC}"
  python3 -m ensurepip --upgrade || {
    echo "${RED}Failed to bootstrap pip. Install pip manually and retry.${NC}"
    exit 1
  }
fi

# Install or upgrade inferops-cli
#
# By default the package is installed from PyPI. To install from a GitHub
# release or source instead, set INFEROPS_INSTALL_SOURCE before piping:
#
#   curl -fsSL https://get.inferops.dev | INFEROPS_INSTALL_SOURCE=git+https://github.com/brassinai/inferops.git#subdirectory=cli sh
#
INSTALL_SOURCE="${INFEROPS_INSTALL_SOURCE:-inferops-cli}"

echo "${YELLOW}Installing ${INSTALL_SOURCE}...${NC}"
pip3 install --upgrade --user "$INSTALL_SOURCE"

# Warn if the install location is not on PATH
USER_BASE=$(python3 -m site --user-base 2>/dev/null || echo "$HOME/.local")
BIN_DIR="${USER_BASE}/bin"

if ! echo "$PATH" | tr ':' '\n' | grep -qx "$BIN_DIR"; then
  echo ""
  echo "${YELLOW}Warning: ${BIN_DIR} is not on your PATH.${NC}"
  echo "Add it by running:"
  echo "  export PATH=\"${BIN_DIR}:\$PATH\""
  echo ""
  echo "To make this permanent, add the line above to your shell profile"
  echo "(~/.bashrc, ~/.zshrc, or ~/.config/fish/config.fish)."
fi

if command -v inferops >/dev/null 2>&1; then
  echo "${GREEN}InferOps CLI installed successfully.${NC}"
  inferops --version
else
  echo ""
  echo "${GREEN}Installation complete.${NC}"
  echo "Open a new shell or run: export PATH=\"${BIN_DIR}:\$PATH\""
fi
