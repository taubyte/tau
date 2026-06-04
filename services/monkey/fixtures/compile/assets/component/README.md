# Component website fixtures (StarlingMonkey wasi:http)

`website_component_test.go` (`-tags "dreaming wasmtime_component"`) deploys each
JS framework's `taubyte-ssr-adapter` output as a Taubyte **website** into a real
`dream` universe and serves it through the substrate's component backend
(`wasmtimehttp` → `wasmtime serve`). It proves the full deploy path end to end —
`build.zip` → `dream` → substrate → component runtime — and that it is
**framework-agnostic**: every framework emits the same `wasi:http` component
shape, so one fixture covers them all.

Each case reads `assets/component/<name>.wasm` (a **raw** component, the adapter's
`handler.component.wasm`). These are large binaries, so they are **not committed**
(`.gitignore`s `*.wasm`); build the ones you want and any that are absent are
skipped. With none present the whole test skips.

## Prerequisites

- `wasmtime` on `PATH` (the backend shells out to `wasmtime serve`).
- The adapter toolchain (Node + `esbuild`/`componentize-js`, fetched on demand).
  See `tools/taubyte-ssr-adapter/README.md` and `docs/framework-support.md`.

## Build the artifacts

Run from the repo root. Each command writes one `<name>.wasm` here. The example
apps live in `tools/taubyte-ssr-adapter/example/`; framework apps you build
yourself (Nest/Vue/Nuxt/Next) follow the recipes in `docs/framework-support.md`.

```sh
ADAPTER="go run ./tools/taubyte-ssr-adapter"
OUT=services/monkey/fixtures/compile/assets/component
EX=tools/taubyte-ssr-adapter/example

# raw node:http, Express 5, Koa 3 — the node:http→fetch bridge (--mode node)
$ADAPTER --mode node --engine starlingmonkey --framework node-http \
    --entry $EX/node-http-app.js --out $OUT/node-http.wasm
$ADAPTER --mode node --engine starlingmonkey --framework express \
    --entry $EX/express-app.js  --out $OUT/express.wasm
$ADAPTER --mode node --engine starlingmonkey --framework koa \
    --entry $EX/koa-app.js      --out $OUT/koa.wasm

# Fastify — async avvio boot deferred to first request (adapter handles it)
$ADAPTER --mode node --engine starlingmonkey --framework fastify \
    --entry $EX/fastify-app.js  --out $OUT/fastify.wasm

# NestJS — compile with tsc first (decorator metadata), then adapt the JS
$ADAPTER --mode node --engine starlingmonkey --framework nestjs \
    --entry dist/main.js        --out $OUT/nestjs.wasm

# Apollo Server (GraphQL, POST /graphql) — serverless-style lazy init
$ADAPTER --mode node --engine starlingmonkey --framework apollo \
    --entry $EX/apollo-app.js   --out $OUT/apollo.wasm

# Bun.serve / Deno.serve — the Bun/Deno global bridges
$ADAPTER --mode bun  --engine starlingmonkey --framework bun \
    --entry $EX/bun-app.js      --out $OUT/bun.wasm
$ADAPTER --mode deno --engine starlingmonkey --framework deno \
    --entry $EX/deno-app.js     --out $OUT/deno.wasm

# Vue 3 SSR (renderToString) and Nuxt 3 (Nitro cloudflare-module) — fetch tier
$ADAPTER --mode fetch --node --engine starlingmonkey --framework vue \
    --entry $EX/vue-ssr-app.js  --out $OUT/vue.wasm
$ADAPTER --mode fetch --node --engine starlingmonkey --framework nuxt \
    --entry .output/server/index.mjs --out $OUT/nuxt.wasm   # see docs for the stub alias

# Next.js (App Router via next-on-pages) — fetch tier
$ADAPTER --mode fetch --node --engine starlingmonkey --framework nextjs \
    --entry .vercel/output/static/_worker.js/index.js --out $OUT/nextjs.wasm
```

The framework names above must match the `name` in `componentCases` (that is the
artifact filename the test loads).

## Run

```sh
go test -tags "dreaming wasmtime_component" -run TestWebsiteComponent_Dreaming \
  -v ./services/monkey/fixtures/compile/
```

`TestWebsiteSSRConfig_Dreaming` (plain `-tags dreaming`, no `wasmtime`/artifacts)
guards the related compiler fix: that a website's `render: ssr` selector survives
compilation into TNS, so SSR sites match every method (e.g. Apollo's POST) from
the first request.
