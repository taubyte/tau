#!/bin/bash

MAX_MEMORY_SIZE=$((1 * 1024))
TARGET_ARCHS=("riscv64" "amd64")
WASM_BUNDLES=()
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

# Create a temporary directory for storing WASM bundles
TEMP_DIR=$(mktemp -d)

for ARCH in "${TARGET_ARCHS[@]}"; do
    BUNDLE_NAME="${ARCH}.wasm"
    BUNDLE_PATH="${TEMP_DIR}/${BUNDLE_NAME}"
    if $C2W_COMMAND --assets /tmp/container2wasm --target-arch="$ARCH" --build-arg VM_MEMORY_SIZE_MB="$MAX_MEMORY_SIZE" --external-bundle "$BUNDLE_PATH"; then
        WASM_BUNDLES+=("$BUNDLE_NAME")
    else
        echo "Failed to create bundle for $ARCH"
        exit 1
    fi
done

if [ ${#WASM_BUNDLES[@]} -gt 0 ]; then
    rm -f assets/runtimes.zip

    # Change to the temporary directory
    pushd "$TEMP_DIR" > /dev/null

    # Create the zip file without including the temporary directory path
    zip -9 -X -Z deflate "${OLDPWD}/assets/runtimes.zip" "${WASM_BUNDLES[@]}"

    # Return to the original directory
    popd > /dev/null

    echo "WASM bundles successfully zipped to runtimes.zip"
else
    echo "No WASM bundles were created, skipping zip."
    exit 1
fi

# Clean up temporary directory
rm -rf "$TEMP_DIR"
