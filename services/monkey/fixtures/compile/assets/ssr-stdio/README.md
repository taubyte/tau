# WASI-stdio server-bundle fixture

`handler.go` is a minimal WASI **command** module (reads request JSON from
stdin, writes response JSON to stdout) used by `website_ssr_stdio_test.go` to
prove the substrate's `wasi-stdio` handler ABI end to end — the same ABI a Javy
(QuickJS) bundle uses, but built from plain Go so no JS toolchain is needed.

The test is gated on a prebuilt `main.wasm` here. Build it as a **command**
module (not `-buildmode=c-shared`, which is for the function ABI):

```sh
tinygo build -o main.wasm -target=wasi .
```

A command module runs `_start` (i.e. `main`), reads stdin and writes stdout,
then exits — exactly what the runtime's wasi-stdio path drives per request. Then:

```sh
go test -tags dreaming -run TestWebsiteSSRStdio_Dreaming -v ./services/monkey/fixtures/compile/
```

Without `main.wasm` the test skips with an explanatory message.

This is the Go stand-in for what `tools/taubyte-ssr-adapter` produces from a
Hono app via Javy: the substrate hosts either identically.
