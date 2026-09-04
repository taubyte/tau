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
