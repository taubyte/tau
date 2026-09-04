#!/usr/bin/env bash
# Run tau's tests on the M4 mini (native arm64/darwin) by cross-compiling a test
# binary here, shipping it plus that package's testdata, and running it there.
#
# WHY THIS EXISTS: this Linux desktop has been taken down four times by test
# runs. The last three were not even limit violations — the cgroup caps held and
# systemd-oomd killed GNOME Shell instead, because a job thrashing at its ceiling
# creates system-wide memory PRESSURE and oomd watches pressure, not limits (see
# scripts/cap). The reliable fix is not another knob: it is to run the heavy
# things somewhere that is not the machine the owner is using.
#
# Usage:
#   scripts/mac-test.sh ./pkg/stats                    # run a package's tests
#   scripts/mac-test.sh ./pkg/engine -test.run TestX -test.v
#   scripts/mac-test.sh --sync ./pkg/tcc/...           # ship testdata first
#
# No Go toolchain and no repo checkout are needed on the Mac — only the compiled
# binary and any testdata the package reads.
#
# WHAT CANNOT RUN HERE, and it is worth knowing before being surprised:
#   - anything that reads THIS repo's source at runtime (pkg/forge/forgetest
#     type-checks the tree with packages.Load, so it needs the source and a
#     toolchain);
#   - anything linux-only or cgo-heavy (pkg/containers, pkg/netguard);
#   - anything that shells out to docker or containerd.
# Those stay here, one package at a time, under scripts/cap.
set -euo pipefail

MAC=${TAU_MAC:-samy@samys-mac-mini.local}   # DHCP moves the numeric address; mDNS name is stable
KEY=${TAU_MAC_KEY:-~/.ssh/macbook_m4_mini}
DEST=tau-arm                                 # relative to the Mac user's home
SSH=(ssh -i "$KEY" -o ConnectTimeout=10)
REPO="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO"

# Ad-hoc codesign entitlements. tau's wasm tier maps and jumps into freshly
# compiled machine code, and on Apple Silicon the kernel kills a process outright
# the instant it fetches from an anonymous RX page without these — a silent
# policy kill with no Go-visible error. Signing must happen ON the Mac; there is
# no cross-signing toolchain from Linux.
ENTITLEMENTS='<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>com.apple.security.cs.allow-jit</key>
	<true/>
	<key>com.apple.security.cs.allow-unsigned-executable-memory</key>
	<true/>
</dict>
</plist>'

SYNC=0
if [ "${1:-}" = "--sync" ]; then SYNC=1; shift; fi

PKG="${1:?usage: scripts/mac-test.sh [--sync] <pkg> [test args...]}"; shift

# The package directory relative to the repo root, e.g. ./pkg/stats -> pkg/stats.
REL="${PKG#./}"
REL="${REL%/...}"
[ -d "$REL" ] || { echo "mac-test: $REL is not a directory in this repo" >&2; exit 2; }

if [ "$SYNC" = "1" ]; then
  echo "== shipping testdata under $REL"
  rsync -avh --prune-empty-dirs -e "ssh -i $KEY" \
    --include='*/' --include='testdata/**' --exclude='*' \
    "./$REL/" "$MAC:$DEST/$REL/"
fi

BIN="$(basename "$REL").test"
OUT="$(mktemp -d)/$BIN"
trap 'rm -rf "$(dirname "$OUT")"' EXIT

echo "== cross-compiling $PKG for darwin/arm64"
# CGO off so the result is a pure-Go static binary that needs nothing on the Mac.
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go test -c -o "$OUT" "$PKG"

echo "== shipping"
"${SSH[@]}" "$MAC" "mkdir -p $DEST/$REL"
scp -q -i "$KEY" "$OUT" "$MAC:$DEST/$REL/$BIN"

echo "== signing + running on $MAC"
"${SSH[@]}" "$MAC" "cd $DEST/$REL && \
  printf '%s' '$ENTITLEMENTS' > ents.plist && \
  codesign -f -s - --entitlements ents.plist '$BIN' >/dev/null 2>&1 || true; \
  ./$BIN $*"
