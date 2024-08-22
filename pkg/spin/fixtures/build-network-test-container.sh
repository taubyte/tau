#!/bin/bash

MAX_MEMORY_SIZE=64
C2W_COMMAND=$(which c2w)
D2OCI_COMMAND=$(which d2oci)

if [ -z "$C2W_COMMAND" ]; then
    echo "c2w is not installed. Please install it from https://github.com/taubyte/container2wasm"
    exit 1
fi

if [ -z "$D2OCI_COMMAND" ]; then
    echo "d2oci is not installed. Please install it from https://github.com/taubyte/container2wasm"
    exit 1
fi

(
    cd /tmp
    git clone https://github.com/taubyte/container2wasm.git
    cd container2wasm
    git pull
)

# make sure we can compile for other architectures
docker run --rm --privileged multiarch/qemu-user-static --reset -p yes

# build squashfs tools
docker buildx build --platform linux/riscv64 -f network-test-container.Dockerfile -t network-test-container .

# Create a temporary directory for storing WASM bundles
TEMP_DIR=$(mktemp -d)

if ! $C2W_COMMAND --target-arch=riscv64 --assets /tmp/container2wasm --build-arg VM_MEMORY_SIZE_MB="$MAX_MEMORY_SIZE" network-test-container "${TEMP_DIR}/network-test-container.wasm"; then
    echo "Failed to create bundle for squashfs"
    exit 1
fi

#cp -fv "${TEMP_DIR}/network-test-container.wasm" .

rm -f network-test-containers.zip

# Change to the temporary directory
pushd "$TEMP_DIR" >/dev/null

# Create the zip file without including the temporary directory path
zip -9 -X -Z deflate "${OLDPWD}/network-test-containers.zip" *.wasm

# Return to the original directory
popd >/dev/null

echo "WASM bundles successfully zipped to tools.zip"

echo "Clean up temporary directory"
rm -rf "$TEMP_DIR"
