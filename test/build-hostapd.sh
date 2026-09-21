#!/usr/bin/env bash
# Builds a wired/MACsec-capable hostapd from the hostap release tarball for
# the e2e test (distro hostapd packages lack the macsec_linux driver).
# Usage: test/build-hostapd.sh [version] [install path]
set -euo pipefail
VER=${1:-2.12}; DEST=${2:-/usr/local/bin/hostapd}
HERE=$(cd "$(dirname "$0")" && pwd)
W=$(mktemp -d); trap 'rm -rf "$W"' EXIT
cd "$W"
curl -sfLO "https://w1.fi/releases/hostapd-${VER}.tar.gz"
tar xzf "hostapd-${VER}.tar.gz"
cd "hostapd-${VER}/hostapd"
cp "$HERE/hostapd.config" .config
make -j"$(nproc)" hostapd >/dev/null
./hostapd -v 2>&1 | head -1 || true   # hostapd -v exits non-zero
install -m 0755 hostapd "$DEST"
