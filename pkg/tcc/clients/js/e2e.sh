#!/usr/bin/env bash
#
# End-to-end test of the generation pipeline: generate the wasm module and the
# TypeScript schema FRESH into an isolated tmp package, drop the hand-written
# runtime and the tests alongside, then typecheck and run the tests against that
# generated code. This validates DSL -> tcc-gen -> generated TS -> wasm as a whole,
# independent of the committed src/gen/schema.ts.
#
# Requires: go, node, and the client's dev deps (tsx/typescript) — reused from the
# real package's node_modules (installed if missing). Run: pkg/tcc/clients/js/e2e.sh
set -euo pipefail

REPO="$(cd "$(dirname "$0")/../../../.." && pwd)"
PKG="$REPO/pkg/tcc/clients/js"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

echo "==> generating wasm + TS into $TMP"
go -C "$REPO" run ./tools/tcc-gen --wasm --out "$TMP/assets"
go -C "$REPO" run ./tools/tcc-gen --ts --out "$TMP/src/gen"

echo "==> dropping runtime + tests + config alongside the generated code"
cp "$PKG"/src/fs.ts "$PKG"/src/loader.ts "$PKG"/src/index.ts "$PKG"/src/tcc.test.ts "$TMP/src/"
cp "$PKG"/package.json "$PKG"/tsconfig.json "$PKG"/tsconfig.build.json "$TMP/"

if [ ! -d "$PKG/node_modules" ]; then
  echo "==> installing client dev deps (one-time)"
  (cd "$PKG" && npm install >/dev/null 2>&1)
fi
ln -s "$PKG/node_modules" "$TMP/node_modules" # reuse deps, no network

echo "==> typecheck (tsc) the generated code"
(cd "$TMP" && npm run build)

echo "==> run tests against the generated code"
(cd "$TMP" && TCC_FIXTURE="$REPO/pkg/tcc/taubyte/v1/fixtures/config" npm test)

echo "==> e2e OK"
