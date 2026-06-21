# SSR server-bundle fixture

`handler.go` is a minimal Taubyte server bundle (an ordinary `//export` HTTP
function) used by `website_ssr_test.go` to prove server-side rendering end to
end: the substrate runtime hosts it as a website's server bundle and calls it
for every dynamic request, server-rendering a path-dependent response.

The test is gated on a prebuilt `main.wasm` in this directory. It is **not**
committed (binary), so build it once with the Taubyte Go→wasm toolchain:

## Option A — Taubyte `go-wasi` image (canonical, matches Monkey)

```sh
docker run --rm -v "$PWD":/src -w /src taubyte/go-wasi:latest \
  sh -c 'tinygo build -o main.wasm -target=wasi -scheduler=none .'
```

## Option B — local TinyGo

```sh
tinygo build -o main.wasm -target=wasi -scheduler=none .
```

The export name (`ssrHandler`) must match the `entry` the test puts in the SSR
manifest. Once `main.wasm` exists here, run:

```sh
go test -tags dreaming -run TestWebsiteSSR_Dreaming -v ./services/monkey/fixtures/compile/
```

Without `main.wasm` the test skips with an explanatory message (no Docker or
TinyGo required to compile the rest of the suite).
