# The two committed components

Real components, built from `../wit/` by a real toolchain. They are committed
because the alternative is a test that needs Rust installed to run at all — and
what they exist to prove is exactly that the host's lowering and lifting agree
with GENERATED bindings, which a fixture built by this repository could not
show.

Both are built against `../wit/` DIRECTLY rather than a copy, so a fixture
cannot drift from the world the host implements — which is the only thing they
are here to prove.

**handler.wasm** is deliberately small and deliberately not a demo — it echoes
the path, prefixes every header it was sent, derives the status from the method
and returns the method with the body, which between them exercise strings, a
list of records and a byte list in both directions. It imports NONE of the
taubyte capabilities, which is itself load-bearing: it is the proof that a
component's import section names what it CALLS rather than what its world
offered.

**guard.wasm** is the only authorizer in the tree, and the only fixture built
against a world that is not `handler`: `taubyte:authorizer`, whose export is
`authorize(question) -> decision` rather than `handle(request) -> response`. It
imports none of the capabilities its world offers, which is the same proof
handler.wasm gives from the other end.

It answers from what it was handed so a test can drive every arm — the target
label `forbidden` gets an empty grant, `undecidable` gets `undecided`, and
anything else gets a FIXED set plus one synthetic entry encoding the whole
question. The fixed set is the contract rather than a shortcut: a guard echoing
only the op it was asked about is broken in a way nothing can detect, and a host
that keeps the answer would let the first operation a workload happened to try
decide every later one.

**cacher.wasm** calls every one of the thirteen capabilities, one branch of its
`handle` per operation, selected by the request path. It is what every
capability's tests drive.

Regenerate after changing the world:

    cd handler        # or cacher
    cargo component build --release --target wasm32-wasip2
    cp target/wasm32-wasip1/release/handler.wasm ../handler.wasm

`wasm32-wasip1` is not a typo: cargo-component builds the core module for
wasip1 and adapts it into a component, so that is where the component lands.

## Adding a capability means editing a Cargo.toml

cargo-component 0.21 does **not** read `../wit/deps/`. Every capability package
has to be named explicitly in each fixture's

    [package.metadata.component.target.dependencies]

with a path. wasm-tools resolves the `deps/` tree on its own and does not need
the block; both are kept working, so a new package under `wit/deps/` is two
lines in two Cargo.toml files as well.

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
