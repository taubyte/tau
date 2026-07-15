# Compiles the guest test fixtures to a single size-optimized reactor wasm that
# imports the "taubyte/sdk" host functions. Built via `make vm-fixtures`, which
# uses the BuildKit `export` stage to drop wasm/ onto the host (committed).
#
# tinygo image ships Go + tinygo + binaryen (wasm-opt).
FROM tinygo/tinygo:0.40.0 AS build

WORKDIR /guest
COPY guest/go.mod guest/go.sum ./
RUN go mod download
COPY guest/ .

# -buildmode=c-shared => wasm reactor (_initialize + //export funcs), matching
# the substrate runtime. Size flags: -opt=z, -no-debug, leaking GC, no scheduler.
# One reactor wasm per guest file, isolated by build tag (tag == file base).
RUN mkdir -p /tmp/out && \
    for f in $(grep -l '//export' *.go); do \
      tag="${f%.go}"; \
      if tinygo build -o "/tmp/out/${tag}.wasm" \
           -target=wasi -buildmode=c-shared \
           -opt=z -no-debug -panic=trap -scheduler=none -gc=leaking \
           -tags "${tag}" . 2>/tmp/err; then \
        wasm-opt -Oz --strip-debug --strip-producers "/tmp/out/${tag}.wasm" -o "/tmp/out/${tag}.wasm"; \
        echo "OK   ${tag}"; \
      else \
        echo "FAIL ${tag}: $(head -1 /tmp/err)"; \
      fi; \
    done && \
    ls /tmp/out/

FROM scratch AS export
COPY --from=build /tmp/out/ /
