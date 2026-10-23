#!/bin/sh
# Builds the function into function.wasm against tau's world at ../../wit.
set -eu
GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o function.core.wasm .
wasm-tools component embed ../../wit --world handler function.core.wasm -o function.embedded.wasm
wasm-tools component new function.embedded.wasm --adapt "$WASI_ADAPTER_REACTOR" -o function.wasm
