#!/usr/bin/env bash
#
# Build the tcc browser wasm with TinyGo (~half the size of the standard Go
# build: ~3.9MB vs ~8.2MB raw). Runs entirely in the tinygo/tinygo container.
#
# TinyGo cannot compile github.com/spf13/afero as-is under its wasm target:
#   - httpFs.go imports net/http (tinygo's net/http js overlay is broken)
#   - OsFs uses os.Chmod / os.Chown (absent) and syscall.EBADFD (absent)
# afero's OsFs is dead code in the browser (we use WithVirtual), so we build a
# patched copy and `replace` it — nothing in the repo is modified.
#
# Usage: pkg/tcc/wasm/tinygo-build.sh [OUT_DIR]
#   OUT_DIR defaults to pkg/tcc/clients/js/assets (the @taubyte/tcc package).
set -euo pipefail

REPO="$(cd "$(dirname "$0")/../../.." && pwd)"
OUT="${1:-$REPO/pkg/tcc/clients/js/assets}"
AFERO_VER="v1.11.0"
IMAGE="tinygo/tinygo:latest"

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

echo "==> patching afero ${AFERO_VER}"
cp -r "$(go env GOMODCACHE)/github.com/spf13/afero@${AFERO_VER}" "$WORK/afero"
chmod -R u+w "$WORK/afero"
rm -f "$WORK/afero/httpFs.go" # drops the sole net/http import
python3 - "$WORK/afero" <<'PY'
import sys, pathlib
d = pathlib.Path(sys.argv[1])
osgo = d / "os.go"
s = osgo.read_text()
s = s.replace("\treturn os.Chmod(name, mode)", "\treturn nil // tinygo: os.Chmod absent; OsFs unused in wasm")
s = s.replace("\treturn os.Chown(name, uid, gid)", "\treturn nil // tinygo: os.Chown absent; OsFs unused in wasm")
osgo.write_text(s)
c = d / "const_win_unix.go"
s = c.read_text()
s = s.replace('import (\n\t"syscall"\n)\n\n', '')
s = s.replace("const BADFD = syscall.EBADFD",
              'import "errors"\n\n// tinygo: syscall.EBADFD absent under wasm; keep an error value.\nvar BADFD error = errors.New("afero: bad file descriptor")')
c.write_text(s)
PY

echo "==> preparing go.mod replace"
cp "$REPO/go.mod" "$WORK/go.mod"
cp "$REPO/go.sum" "$WORK/go.sum"
go mod edit -replace "github.com/spf13/afero=/afero" "$WORK/go.mod"

mkdir -p "$OUT"
chmod 777 "$OUT" # rootless docker maps the container user to a subuid
# remove any prior (host-owned) outputs so the container can create fresh ones
rm -f "$OUT/tcc.wasm" "$OUT/wasm_exec.js"

echo "==> tinygo build (in container)"
docker run --rm \
  -v "$REPO":/src \
  -v "$WORK/go.mod":/src/go.mod \
  -v "$WORK/go.sum":/src/go.sum \
  -v "$WORK/afero":/afero \
  -v "$OUT":/out \
  -e "GOFLAGS=-mod=mod -buildvcs=false" \
  -w /src \
  "$IMAGE" \
  tinygo build -o /out/tcc.wasm -target wasm ./pkg/tcc/wasm

echo "==> copying tinygo's wasm_exec.js"
docker run --rm "$IMAGE" \
  sh -c 'cat "$(find / -name wasm_exec.js -path "*tinygo*" 2>/dev/null | head -1)"' > "$OUT/wasm_exec.js"

echo "built $OUT/tcc.wasm ($(du -h "$OUT/tcc.wasm" | cut -f1)) + wasm_exec.js"
