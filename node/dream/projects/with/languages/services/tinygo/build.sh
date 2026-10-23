#!/bin/sh
# Builds the function into function.wasm against tau's world at ../../wit.
set -eu

# wit-bindgen-go 0.7.0 prints the world it re-parses for its custom section
# without escaping a keyword, so `import taubyte:%stream/publisher` comes out
# as `taubyte:stream/publisher` and fails to parse. The bindings are generated
# from a copy with that one package renamed; the component itself is built
# against the real world below, and this function calls nothing in it.
rm -rf gen build
mkdir -p build
cp -R ../../wit build/bindgen
sed -i 's/taubyte:%stream/taubyte:streamed/' build/bindgen/handler.wit build/bindgen/deps/taubyte-stream/*.wit
wit-bindgen-go generate --world taubyte:handler/handler -o gen build/bindgen

# TinyGo's wasip2 runtime imports WASI 0.2.0 from its own bundled WIT, so the
# world the component is built against is tau's handler plus those imports.
mkdir -p build/wit/deps/taubyte-handler
cp -R ../../wit/deps/. build/wit/deps/
cp ../../wit/handler.wit build/wit/deps/taubyte-handler/
cp -R "$(tinygo env TINYGOROOT)/lib/wasi-cli/wit" build/wit/deps/tinygo-cli
for d in build/wit/deps/tinygo-cli/deps/*; do
	mv "$d" "build/wit/deps/tinygo-${d##*/}"
done
rmdir build/wit/deps/tinygo-cli/deps
cat >build/wit/world.wit <<'WIT'
package tau:tinygo;

world function {
  include taubyte:handler/handler@0.1.0;
  include wasi:cli/imports@0.2.0;
}
WIT
tinygo build -target=wasip2 --wit-package build/wit --wit-world function -o function.wasm .
