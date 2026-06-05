#!/bin/sh
# install.sh — heir CLI installer
# Usage: curl -sSL https://raw.githubusercontent.com/tardigradeproj/heir/refs/heads/main/install.sh | HEIR_VERSION=latest sh
set -e

GITHUB_REPO="tardigradeproj/heir"
BINARY_NAME="heir"
DEFAULT_INSTALL_DIR="/usr/local/bin"

info()  { printf '[heir] %s\n' "$*"; }
error() { printf '[heir] error: %s\n' "$*" >&2; exit 1; }

check_deps() {
  for cmd in curl tar; do
    command -v "$cmd" >/dev/null 2>&1 || error "Required command not found: $cmd"
  done
}

detect_os() {
  case "$(uname -s)" in
    Linux) OS=Linux ;;
    *)     error "Unsupported operating system: $(uname -s). heir supports Linux only." ;;
  esac
}

detect_arch() {
  case "$(uname -m)" in
    x86_64|amd64)  ARCH=x86_64 ;;
    aarch64|arm64) ARCH=arm64 ;;
    *)             error "Unsupported architecture: $(uname -m). Supported: x86_64, arm64." ;;
  esac
}

resolve_version() {
  HEIR_VERSION="${HEIR_VERSION:-latest}"
  if [ "$HEIR_VERSION" = "latest" ]; then
    info "Resolving latest release..."
    HEIR_VERSION="$(curl -sSfL "https://api.github.com/repos/${GITHUB_REPO}/releases/latest" \
      | grep '"tag_name"' \
      | sed 's/.*"tag_name": *"\([^"]*\)".*/\1/')"
    [ -n "$HEIR_VERSION" ] || error "Failed to resolve latest release. Check your network connection."
  fi
  # Normalize: ensure version has a leading 'v'
  case "$HEIR_VERSION" in
    v*) ;;
    *)  HEIR_VERSION="v${HEIR_VERSION}" ;;
  esac
}

main() {
  check_deps
  detect_os
  detect_arch
  resolve_version

  INSTALL_DIR="${HEIR_INSTALL_DIR:-${DEFAULT_INSTALL_DIR}}"
  ARCHIVE="tardigrade_heir_${OS}_${ARCH}.tar.gz"
  BASE_URL="https://github.com/${GITHUB_REPO}/releases/download/${HEIR_VERSION}"

  info "Installing heir ${HEIR_VERSION} (${OS}/${ARCH})..."

  TMP_DIR="$(mktemp -d)"
  trap 'rm -rf "$TMP_DIR"' EXIT

  info "Downloading ${ARCHIVE}..."
  curl -sSfL "${BASE_URL}/${ARCHIVE}" -o "${TMP_DIR}/${ARCHIVE}" \
    || error "Download failed. Verify that version '${HEIR_VERSION}' exists and supports ${OS}/${ARCH}."

  info "Extracting ${ARCHIVE}..."
  tar -xzf "${TMP_DIR}/${ARCHIVE}" -C "${TMP_DIR}"
  [ -f "${TMP_DIR}/${BINARY_NAME}" ] || error "Binary '${BINARY_NAME}' not found inside archive."

  if [ ! -d "${INSTALL_DIR}" ]; then
    mkdir -p "${INSTALL_DIR}" 2>/dev/null \
      || sudo mkdir -p "${INSTALL_DIR}" \
      || error "Cannot create install directory: ${INSTALL_DIR}"
  fi

  info "Installing to ${INSTALL_DIR}/${BINARY_NAME}..."
  if [ -w "${INSTALL_DIR}" ]; then
    mv "${TMP_DIR}/${BINARY_NAME}" "${INSTALL_DIR}/${BINARY_NAME}"
    chmod 755 "${INSTALL_DIR}/${BINARY_NAME}"
  else
    sudo mv "${TMP_DIR}/${BINARY_NAME}" "${INSTALL_DIR}/${BINARY_NAME}"
    sudo chmod 755 "${INSTALL_DIR}/${BINARY_NAME}"
  fi

  printf '\n[heir] heir %s installed to %s\n' "${HEIR_VERSION}" "${INSTALL_DIR}/${BINARY_NAME}"
#  printf '[heir] Run '\''heir version'\'' to verify the installation.\n'
}

main
