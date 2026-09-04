# handler.wasm

A real component, built from `../wit/handler.wit` by a real toolchain. It is
committed because the alternative is a test that needs Rust installed to run at
all — and what it exists to prove is exactly that the host's lowering and
lifting agree with GENERATED bindings, which a fixture built by this repository
could not show.

It is built against `../wit/handler.wit` DIRECTLY rather than a copy, so the
fixture cannot drift from the world the host implements — which is the only
thing it is here to prove.

Regenerate after changing the world:

    cd handler
    cargo component build --release --target wasm32-wasip2
    cp target/wasm32-wasip1/release/handler.wasm ../handler.wasm

`wasm32-wasip1` is not a typo: cargo-component builds the core module for
wasip1 and adapts it into a component, so that is where the component lands.

The guest is deliberately small and deliberately not a demo — it echoes the
path, prefixes every header it was sent, derives the status from the method and
returns the method with the body, which between them exercise both strings,
the list of records and the byte list in both directions.

## The standard world

There is no committed fixture for `wasi:http/incoming-handler`, and that is a
gap rather than a decision. Building one needs the wasi WIT packages, which
cargo-component fetches from a registry — and the copy wazy vendors is trimmed
for its own conformance runs (its `cli.wit` imports a `wasi:clocks/timezone`
its `clocks.wit` does not define), so it does not merge as a whole.

`wasm_http_test.go` therefore drives wazy's own prebuilt standard-world
component out of the module cache. It is a real component built by a real
toolchain, which is what the adapter has to be tested against; what it is not
is ours, so the test says where it came from and skips with a reason if a wazy
upgrade moves it.

Closing this means vendoring the upstream wasi WIT (wasi-http, wasi-io,
wasi-clocks, wasi-random and a complete wasi-cli) under a proxy fixture.
