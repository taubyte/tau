# The committed components

Real components, built from `../wit/` by a real toolchain. They are committed
because the alternative is a test that needs Rust installed to run at all — and
what they exist to prove is exactly that the host's lowering and lifting agree
with GENERATED bindings, which a fixture built by this repository could not
show.

Every one is built against `../wit/` DIRECTLY rather than a copy, so a fixture
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

**admitter.wasm** is the instance validator: `taubyte:instance-validator`,
exporting `admit(opening) -> verdict`. It replaces a stopgap rather than nothing
— until its world was built a validator had to BE a handler, told which instance
by a header and answering with an HTTP status, which had no room for the
difference between a refusal and a refusal to decide.

It answers from the instance name (`denied`, `unsure`, `echo`, anything else).
The echo arm exists because `admitted` carries nothing: a yes has no room to show
that the record arrived intact, so the refusal's string is borrowed to hand the
opening back.

**shaper.wasm** is the gateway transformer: `taubyte:gateway-transformer`,
exporting `transform(request) -> outcome`. Unlike the guard and the admitter it
is POOLED — it runs on every request and nothing about its answer can be kept —
so it is started by the ordinary Start path and the pool's warm slots are what
keep a ~13ms cold start off the edge.

It deliberately CHANGES what it forwards, adding a header and prefixing the body.
Reading a `request` back out of a guest is something nothing else in this tree
does, and a pass-through fixture would pass whether or not the host read the
forwarded request at all.

**picker.wasm** is the gateway router: `taubyte:gateway-router`, exporting
`route(request) -> decision`. Pooled like the shaper, for the same reason — it
decides per request.

It drives all three arms (a destination, an answer it served itself, and
"nothing handles this"), and the destination it returns ECHOES the path it was
given: a router that ignored its argument and returned a constant would satisfy
every assertion about which arm was taken.

**usher.wasm** is the proxy router: `taubyte:proxy-router`, exporting
`route(connection, list<u8>) -> decision`. It is what a proxy names instead of a
fixed forward, and unlike the gateway's router it is handed a source address and
some bytes rather than a parsed request — a raw connection carries nothing else.

It drives all three arms, chosen by what it has been shown: fewer than four
bytes gets `more(4)`, which is how a router asks to see enough of a protocol to
recognise it; `nope` gets a refusal; `pg` gets a destination in another
namespace, on a named port. Anything else gets a destination whose NAME echoes
the host it was asked about and the four bytes it decided from, so a router that
ignored its arguments would not pass.

**sieve.wasm** is the proxy transformer: `taubyte:proxy-transformer`, exporting
`transform(traffic) -> verdict`. It is called per chunk and per direction, and
what it remembers between chunks is a blob the HOST carries — which is what
keeps it a pooled instance rather than one pinned to a connection for the life
of a psql session.

It BUFFERS, and that is the point: inbound bytes are held until a newline
arrives and the whole line goes out uppercased. A pass-through fixture would
pass whether or not the host carried the state back at all, which is the one
thing this world's design rests on. A line reading `stop` closes the connection,
and outbound chunks are prefixed so a test can tell the directions apart.

**cacher.wasm** calls every one of the capabilities, one branch of its `handle`
per operation, selected by the request path. It is what every capability's tests
drive.

Its `infer` branch asks for a MILLION tokens on every call, deliberately: the
ceiling belongs to the `model` resource and the host clamps to it, so what that
branch proves is that a guest cannot raise a limit its author wrote.

Its `infer-tools` branch is one turn of an agent loop written by hand, which is
the whole point of putting tools in that interface: the model says what it wants
run and the CALLER runs it — nothing in the host does. It sends a tool, echoes
back every call the model asked for with its id, and so shows a call crossing in
both directions rather than only which arm was taken.

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
