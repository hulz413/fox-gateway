#!/bin/sh
set -eu

REPO="${REPO:-hulz413/fox-gateway}"
BINARY_NAME="${BINARY_NAME:-fox-gateway}"
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"
PROFILE_FILE="${PROFILE_FILE:-$HOME/.profile}"
VERSION="${VERSION:-}"
DOWNLOAD_BASE_URL="${DOWNLOAD_BASE_URL:-https://github.com/$REPO/releases}"
UNAME_S="${UNAME_S:-$(uname -s)}"
UNAME_M="${UNAME_M:-$(uname -m)}"

log() {
  printf '%s\n' "$*"
}

fail() {
  printf 'Error: %s\n' "$*" >&2
  exit 1
}

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    fail "required command not found: $1"
  fi
}

path_contains() {
  case ":${PATH:-}:" in
    *":$1:"*) return 0 ;;
    *) return 1 ;;
  esac
}

profile_has_path() {
  if [ ! -f "$PROFILE_FILE" ]; then
    return 1
  fi

  case "$INSTALL_DIR" in
    "$HOME/.local/bin")
      grep -Fqs '$HOME/.local/bin' "$PROFILE_FILE" || grep -Fqs "$HOME/.local/bin" "$PROFILE_FILE"
      ;;
    *)
      grep -Fqs "$INSTALL_DIR" "$PROFILE_FILE"
      ;;
  esac
}

ensure_path() {
  if path_contains "$INSTALL_DIR"; then
    PATH_UPDATED=0
    return 0
  fi

  PATH_UPDATED=1
  if [ "$INSTALL_DIR" = "$HOME/.local/bin" ]; then
    export_line='export PATH="$HOME/.local/bin:$PATH"'
  else
    export_line="export PATH=\"$INSTALL_DIR:\$PATH\""
  fi

  if ! profile_has_path; then
    mkdir -p "$(dirname "$PROFILE_FILE")"
    touch "$PROFILE_FILE"
    printf '\n%s\n' "$export_line" >> "$PROFILE_FILE"
    log "Added $INSTALL_DIR to PATH in $PROFILE_FILE"
  fi

  if [ -r "$PROFILE_FILE" ]; then
    set +e
    # shellcheck disable=SC1090
    . "$PROFILE_FILE" >/dev/null 2>&1
    set -e
  fi
}

detect_target() {
  case "$UNAME_S" in
    Linux)
      case "$UNAME_M" in
        x86_64|amd64) printf 'linux_amd64' ;;
        *) fail "unsupported Linux architecture: $UNAME_M" ;;
      esac
      ;;
    Darwin)
      case "$UNAME_M" in
        x86_64|amd64) printf 'darwin_amd64' ;;
        arm64|aarch64) printf 'darwin_arm64' ;;
        *) fail "unsupported macOS architecture: $UNAME_M" ;;
      esac
      ;;
    *)
      fail "unsupported operating system: $UNAME_S"
      ;;
  esac
}

build_download_url() {
  asset_name="$1"
  if [ -n "$VERSION" ]; then
    printf '%s/download/%s/%s' "$DOWNLOAD_BASE_URL" "$VERSION" "$asset_name"
  else
    printf '%s/latest/download/%s' "$DOWNLOAD_BASE_URL" "$asset_name"
  fi
}

main() {
  PATH_UPDATED=0

  require_command curl
  require_command chmod
  require_command mv
  require_command mkdir

  target="$(detect_target)"
  asset_name="${BINARY_NAME}_${target}"
  download_url="$(build_download_url "$asset_name")"

  mkdir -p "$INSTALL_DIR"
  tmp_file="$INSTALL_DIR/.${BINARY_NAME}.tmp.$$"
  trap 'rm -f "$tmp_file"' EXIT INT TERM HUP

  log "Installing $BINARY_NAME for $target"
  log "Downloading $download_url"
  curl -fsSL "$download_url" -o "$tmp_file"
  chmod +x "$tmp_file"
  mv "$tmp_file" "$INSTALL_DIR/$BINARY_NAME"

  ensure_path

  log "Installed to $INSTALL_DIR/$BINARY_NAME"
  if path_contains "$INSTALL_DIR"; then
    log "Current shell PATH includes $INSTALL_DIR"
  fi
  if [ "$PATH_UPDATED" -eq 1 ]; then
    log "Updated PATH in $PROFILE_FILE"
    log "If '$BINARY_NAME' is not found in your current shell, run: source $PROFILE_FILE"
    log "Or open a new shell session."
  fi
}

main "$@"
