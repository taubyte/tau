#!/bin/bash

# We shoudl release the wasm file

SCRIPT_PATH="$(readlink -f "${BASH_SOURCE[0]}")"

SCRIPT_DIR="$(cd "$(dirname "$SCRIPT_PATH")" && pwd)"

cd "${SCRIPT_DIR}"

go run .

cd -