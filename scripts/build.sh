#!/bin/sh
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
ROOT_DIR=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)

BINARY_NAME="${BINARY_NAME:-fox-gateway}"
GOOS="${GOOS:-$(go env GOOS)}"
GOARCH="${GOARCH:-$(go env GOARCH)}"
CGO_ENABLED="${CGO_ENABLED:-1}"
VERSION="${VERSION:-dev}"
COMMIT="${COMMIT:-$(git -C "$ROOT_DIR" rev-parse HEAD 2>/dev/null || printf 'unknown')}"
DATE="${DATE:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"
OUTPUT_PATH="${OUTPUT_PATH:-$ROOT_DIR/$BINARY_NAME}"

log() {
  printf '%s\n' "$*"
}

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    printf 'Error: required command not found: %s\n' "$1" >&2
    exit 1
  fi
}

require_command go
require_command git
require_command date
require_command chmod
require_command mv

TMP_OUTPUT="$ROOT_DIR/.${BINARY_NAME}.tmp"
trap 'rm -f "$TMP_OUTPUT"' EXIT INT TERM HUP

log "Building $BINARY_NAME for ${GOOS}/${GOARCH}"
log "Output: $OUTPUT_PATH"

(
  cd "$ROOT_DIR"
  env \
    CGO_ENABLED="$CGO_ENABLED" \
    GOOS="$GOOS" \
    GOARCH="$GOARCH" \
    go build \
      -trimpath \
      -ldflags "-s -w -X main.version=$VERSION -X main.commit=$COMMIT -X main.date=$DATE" \
      -o "$TMP_OUTPUT" \
      ./cmd/server
)

chmod +x "$TMP_OUTPUT"
mv "$TMP_OUTPUT" "$OUTPUT_PATH"
