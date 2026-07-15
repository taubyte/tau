#!/usr/bin/env bash
# Rebuild the vm-low-orbit guest wasm fixtures. Outputs are committed under
# wasm/ (<name>.wasm for Go, <name>_rs.wasm for Rust). Invoked by `make vm-fixtures`.
#
# Go   : tinygo -buildmode=c-shared reactor modules, built in a container.
# Rust : cargo cdylib (wasm32-unknown-unknown), built natively (rustup + the
#        wasm32-unknown-unknown target required).
set -euo pipefail
DIR="$(cd "$(dirname "$0")" && pwd)"
mkdir -p "$DIR/wasm"

echo "==> Go fixtures (tinygo, container)"
DOCKER_BUILDKIT=1 docker build --no-cache -f "$DIR/Dockerfile" \
	--target=export --output="$DIR/wasm" "$DIR"

echo "==> Rust fixtures (cargo, native)"
cd "$DIR/rust"
mkdir -p src
for f in srcs/*.rs; do
	name="$(basename "$f" .rs)"
	cp "$f" src/lib.rs
	cargo build --target wasm32-unknown-unknown --release >/dev/null
	cp target/wasm32-unknown-unknown/release/taubyte_vm_test_guest.wasm \
		"$DIR/wasm/${name}_rs.wasm"
	echo "    ${name}_rs.wasm"
done
rm -f src/lib.rs
echo "==> done: $(ls "$DIR"/wasm/*.wasm | wc -l) wasm files"
