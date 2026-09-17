# cacher.wasm

The guest half of every capability in `../../wit/`, built by a real toolchain.
It imports all thirteen — cache, sql, kv, document, vector, storage, stream,
bus, queue, workflow, secret, channel and workload — one `handle` branch per
operation. It is committed for the reason `../handler`'s is: the
alternative is a test that needs Rust installed to run at all, and what it
proves — that the host's lowering and lifting agree with GENERATED bindings —
is the one thing no in-process fake can show.

It is built against the canonical wit DIRECTLY rather than a copy, so the
fixture cannot drift from the world the host implements.

Regenerate after changing the world:

    cd pkg/invoke/wasm/fixtures/cacher
    cargo component build --release --target wasm32-wasip2
    cp target/wasm32-wasip1/release/cacher.wasm ../cacher.wasm

`wasm32-wasip1` is not a typo: cargo-component builds the core module for wasip1
and adapts it into a component, so that is where the component lands.

## What it does

The request's path is `/<op>/<label>/<key>` and its body is the value, so one
component drives every interface. Every branch answers with either the value or
the NAME of the error arm it got back — so a test asserting on the body is
asserting WHICH refusal the host produced, which is the half that would
otherwise be untestable.

Where a call has more than two knobs, they come off HEADERS rather than off the
path: `x-taubyte-content-type` and `x-meta-*` for a put, and
`x-taubyte-delimiter`, `x-taubyte-max-keys` and `x-taubyte-token` for a listing.
That keeps the path one shape for every branch while still exercising the whole
request record.

`roundtrip` writes and reads in one call, which is the only branch that proves
the bytes a guest wrote are the bytes it gets.
