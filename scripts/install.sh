#!/usr/bin/env bash
#
# TrustPanel one-shot installer.
#
# Turns a bare server into a TrustPanel deployment by pulling the release
# binaries from GitHub — no manual scp. It downloads `trustpanel`, `sing-box`
# (a with_v2ray_api build) and, if present, `trusttunnel_endpoint` from this
# project's GitHub Release, verifies them all against the release SHA256SUMS,
# then runs `trustpanel bootstrap`.
#
# Usage (run as root on the target server):
#
#   # multi-node: this box becomes the control plane + first exit node
#   curl -fsSL https://raw.githubusercontent.com/br0therfromanothermother/TrustPanel/main/scripts/install.sh \
#     | sudo bash -s -- --domain example.com --brand ExampleCDN \
#         --reality-sni www.example-cdn-target.com
#
#   # single-box: control plane + client-facing entry, local egress
#   curl -fsSL .../install.sh | sudo bash -s -- --domain example.com --brand ExampleCDN --single
#
# Required: --domain, --brand. --reality-sni is required unless --single (a real
# TLS 1.3 site to borrow for the Reality handshake). --single needs the
# trusttunnel_endpoint asset in the release. Everything after the listed flags
# is forwarded verbatim to `trustpanel bootstrap` (e.g. --admin-user,
# --acme-staging). Node ids are internal and auto-generated — there's normally
# no reason to pass --node-id. Running a fork's own release: set TRUSTPANEL_REPO
# (env var, not a flag). See README.md for the full flag reference and every
# value this script or `trustpanel bootstrap` defaults when omitted.
#
set -euo pipefail

# --- pinned dependencies -----------------------------------------------------
# GitHub repo that publishes the TrustPanel release. Override with the
# TRUSTPANEL_REPO env var (e.g. running a fork's own release).
REPO_DEFAULT="br0therfromanothermother/TrustPanel"

# sing-box ships as a release asset built with the with_v2ray_api tag (entry-node
# per-user stats); the upstream binary omits it, so we never pull SagerNet stock.

# --- defaults / args ---------------------------------------------------------
REPO="${TRUSTPANEL_REPO:-$REPO_DEFAULT}"
TAG="${TRUSTPANEL_TAG:-latest}"
DOMAIN=""
BRAND=""
REALITY_SNI=""
PUBLIC_IP=""
SINGLE=0
PASSTHROUGH=()

die() { echo "install: $*" >&2; exit 1; }
have() { command -v "$1" >/dev/null 2>&1; }

while [[ $# -gt 0 ]]; do
  case "$1" in
    --domain)      DOMAIN="$2"; shift 2 ;;
    --brand)       BRAND="$2"; shift 2 ;;
    --reality-sni) REALITY_SNI="$2"; shift 2 ;;
    --public-ip)   PUBLIC_IP="$2"; shift 2 ;;
    --tag)         TAG="$2"; shift 2 ;;
    --single)      SINGLE=1; PASSTHROUGH+=("$1"); shift ;;
    *)             PASSTHROUGH+=("$1"); shift ;;
  esac
done

[[ "${EUID}" -eq 0 ]] || die "must run as root"
[[ -n "$DOMAIN" ]] || die "--domain is required"
[[ -n "$BRAND" ]] || die "--brand is required (this becomes the camouflage site's identity — pick your own, don't reuse an example)"
if [[ "$SINGLE" -eq 0 ]]; then
  [[ -n "$REALITY_SNI" ]] || die "--reality-sni is required for a multi-node exit (a real TLS 1.3 site to borrow for the Reality handshake)"
fi
[[ "$REPO" != "OWNER/REPO" ]] || die "set the release repo via the TRUSTPANEL_REPO env var"
have curl || die "curl is required"
have tar  || die "tar is required"
have sha256sum || die "sha256sum is required"

# --- arch detection ----------------------------------------------------------
case "$(uname -m)" in
  x86_64|amd64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) die "unsupported architecture: $(uname -m)" ;;
esac

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

asset_url() { # $1 = asset filename
  if [[ "$TAG" == "latest" ]]; then
    echo "https://github.com/${REPO}/releases/latest/download/$1"
  else
    echo "https://github.com/${REPO}/releases/download/${TAG}/$1"
  fi
}

echo "install: repo=${REPO} tag=${TAG} arch=${ARCH} domain=${DOMAIN}"

# --- fetch + verify trustpanel (and optional trusttunnel) --------------------
echo "install: downloading release checksums"
curl -fsSL -o "$WORK/SHA256SUMS" "$(asset_url SHA256SUMS)" \
  || die "could not fetch SHA256SUMS from the ${TAG} release of ${REPO}"

fetch_verified() { # $1 = asset filename -> downloaded to $WORK/$1, checksum-verified
  local name="$1"
  echo "install: downloading $name"
  curl -fSL --retry 3 -o "$WORK/$name" "$(asset_url "$name")" || return 1
  ( cd "$WORK" && grep " $name\$" SHA256SUMS | sha256sum -c - ) \
    || die "checksum mismatch for $name"
}

TP_ASSET="trustpanel-linux-${ARCH}"
fetch_verified "$TP_ASSET" || die "could not download $TP_ASSET"
install -m0755 "$WORK/$TP_ASSET" /usr/local/bin/trustpanel
echo "install: trustpanel installed -> /usr/local/bin/trustpanel"

# trusttunnel_endpoint is optional for the exit bootstrap (only staged for later
# entry provisioning, and only published for amd64). Skip cleanly if absent.
TT_ASSET="trusttunnel_endpoint-linux-amd64"
TT_BIN=""
if [[ "$ARCH" == "amd64" ]] && grep -q " ${TT_ASSET}\$" "$WORK/SHA256SUMS"; then
  if fetch_verified "$TT_ASSET"; then
    TT_BIN="$WORK/$TT_ASSET"
    chmod +x "$TT_BIN"
    echo "install: trusttunnel_endpoint staged for entry provisioning"
  fi
else
  echo "install: trusttunnel_endpoint not in this release — skipping (optional)"
fi

# --- fetch + verify sing-box (with_v2ray_api build, from our release) ---------
SB_ASSET="sing-box-linux-${ARCH}"
fetch_verified "$SB_ASSET" || die "could not download $SB_ASSET"
chmod +x "$WORK/$SB_ASSET"
SB_BIN="$WORK/$SB_ASSET"

# --- public IP (bootstrap requires it) ---------------------------------------
if [[ -z "$PUBLIC_IP" ]]; then
  PUBLIC_IP="$(curl -fsSL --max-time 10 https://api.ipify.org 2>/dev/null || true)"
  [[ -z "$PUBLIC_IP" ]] && PUBLIC_IP="$(ip -4 route get 1.1.1.1 2>/dev/null | sed -n 's/.* src \([0-9.]*\).*/\1/p')"
  [[ -n "$PUBLIC_IP" ]] || die "could not auto-detect public IP; pass --public-ip"
  echo "install: detected public IP ${PUBLIC_IP}"
fi

# --- run bootstrap -----------------------------------------------------------
echo "install: running trustpanel bootstrap"
BOOT_ARGS=(bootstrap
  --domain "$DOMAIN"
  --brand "$BRAND"
  --singbox-bin "$SB_BIN")
[[ -n "$REALITY_SNI" ]] && BOOT_ARGS+=(--reality-sni "$REALITY_SNI")
BOOT_ARGS+=(--public-ip "$PUBLIC_IP")
[[ -n "$TT_BIN" ]] && BOOT_ARGS+=(--trusttunnel-bin "$TT_BIN")
BOOT_ARGS+=("${PASSTHROUGH[@]+"${PASSTHROUGH[@]}"}")

exec /usr/local/bin/trustpanel "${BOOT_ARGS[@]}"
