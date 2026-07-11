#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SRC_DIR="${TRUSTTUNNEL_SRC_DIR:-/tmp/TrustTunnel}"
OUT_DIR="${TRUSTTUNNEL_OUT_DIR:-$ROOT_DIR/bin}"
REPO_URL="${TRUSTTUNNEL_REPO_URL:-https://github.com/TrustTunnel/TrustTunnel.git}"
PATCH_FILE="$ROOT_DIR/patches/trusttunnel-reverse-proxy-private-origin.patch"

mkdir -p "$OUT_DIR"

if [[ ! -d "$SRC_DIR/.git" ]]; then
  git clone "$REPO_URL" "$SRC_DIR"
fi

cd "$SRC_DIR"
git apply --reverse --check "$PATCH_FILE" >/dev/null 2>&1 || git apply "$PATCH_FILE"

cargo build -p trusttunnel_endpoint --release
install -m 0755 target/release/trusttunnel_endpoint "$OUT_DIR/trusttunnel_endpoint"
"$OUT_DIR/trusttunnel_endpoint" --version
