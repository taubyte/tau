# JavaScript runtime roadmap (toward arbitrary Next.js)

Where the JS hosting runtime is and the ordered path to fuller framework
support, including the engine swap. Status legend: ✅ done · ◑ partial · ⬜ planned.

## Today

- ✅ **Serving**: static + SSR routing + `/api`, two handler ABIs (`function`,
  `wasi-stdio`), proven via `dreaming` tests.
- ✅ **Javy tier**: Web-API polyfill (`Request`/`Response`/`Headers`/`URL`,
  `btoa`/`atob`/`structuredClone`, insecure `crypto.randomUUID`) + Node shims
  (`process`/`Buffer`/`global`/timers) + event-loop. **Hono renders end to end.**
- ◑ Remix/SvelteKit/Nuxt/SolidStart: same fetch path; expected to work, patch
  polyfill gaps per app.
- ✅ Next.js (App Router) via `next-on-pages` + `--engine starlingmonkey`:
  static/prerendered, dynamic React SSR, and `GET`/`POST` edge API routes all
  work (validated against a real Next.js 14 app — see §5).
- ✅ Node HTTP-server frameworks (Express/Koa/…) via `--mode node` and Bun apps
  (`Bun.serve`) via `--mode bun`: a `node:http` → fetch bridge / a `Bun` global on
  the component tier, with node-builtin shims. Express 4, Koa 3 and `Bun.serve`
  validated end to end (see §6). HTTP request-handling, not a full Node/Bun runtime
  (no fs/net/child_process).

## Ordered plan

### 1. Solidify the fetch tier (Hono → Remix/SvelteKit) — ◑
Harden `web.js`/`node.js` against what each framework's bundle actually calls.
Cheap, immediate breadth, no substrate changes. Validate per app on real builds.

### 2. Engine/ABI seam — ✅ (this step)
The manifest carries the handler **ABI**; the substrate dispatches on it and
**fails fast** on one it can't run. Added `ABIComponent` ("component") as the
slot for a richer, component-model JS engine, so bundles can target it and the
platform can grow a backend without breaking the Javy tier. No engine swap
required to land the seam.

### 3. Streaming — ◑ (partly already done; the rest is coupled)
- **Function ABI already streams.** Those handlers write straight to the HTTP
  response writer via the go-sdk HTTP event, so incremental writes reach the
  client as produced — no work needed.
- **wasi-stdio is buffered.** `serveSSRStdio` runs the module to completion, then
  writes the JSON envelope. True streaming there needs two things:
  1. **VM-layer plumbing** — run `_start` in a goroutine with stdout on an
     os.Pipe, stream the read side to the response, and close only the write-end
     on completion to signal EOF. The current `pipe` closes both ends, so this
     needs a write-half close in `pkg/vm/service/wazero`. A header-preamble wire
     format (`{"status","headers"}\n` then raw body) lets status/headers go out
     before the body.
  2. **A producer** — the JS handler must emit incrementally. Hono/`fetch` buffer
     the `Response`; real streaming wants `ReadableStream`, which comes with the
     richer engine (step 4). A hand-written stdio handler can stream once (1)
     lands.

  So stdio streaming is best built **with** the VM-engine work, where the pipe
  lifecycle can be verified — not blind. The seam is the engine registry (step 2):
  a streaming engine slots in alongside the buffered one.

### 4. Engine swap → component-model JS engine (StarlingMonkey) — ✅ (built + proven)
Javy/QuickJS is a small engine with a thin Web-API surface we polyfill by hand,
and its bytecode compiler **crashes** on heavy bundles (React `react-dom/server`
traps with "stack underflow"). Real Next.js (and rich frameworks) want a
near-browser surface: full WebCrypto, streams, `fetch`, `AsyncLocalStorage`.
**StarlingMonkey** (SpiderMonkey on WASM) provides that, and runs the exact React
SSR that crashed QuickJS.

**The obstacle that shaped the design:** StarlingMonkey is a **WebAssembly
Component** (Component Model + WASI Preview 2, `wasi:http`). wazero (Taubyte's
default VM) runs **core modules + WASI Preview 1** and can't host components —
**and neither can the `wasmtime-go` bindings** (verified: v25 exposes no
Component Model API). So an in-process Go host is not available.

**What shipped:** the reference backend `services/substrate/components/http/
website/wasmtimehttp` (build tag `wasmtime_component`) shells out to the
**`wasmtime` CLI**, whose `wasmtime serve` implements the complete `wasi:http`
host. It lazily spawns one `wasmtime serve` per component (keyed by DAG cid,
cached) and reverse-proxies requests to it; `init()` registers it via
`website.RegisterComponentRuntime`, so the wasmtime dependency stays out of the
default (pure-Go/wazero) build. Until a backend registers, `component` assets
fail fast.

**Producer:** `taubyte-ssr-adapter --engine starlingmonkey` bundles the app with
esbuild (no Web-API polyfill — StarlingMonkey is native), wraps it in a
fetch-event shim, and runs `jco componentize` against the vendored
`wasi:http/proxy` WIT to emit a component; the manifest records `abi:"component"`
and the raw `.wasm` handler.

**Validated end to end** (esbuild + jco/StarlingMonkey + wasmtime 27): a fetch
handler and a React `renderToString` page both componentize, serve under
`wasmtime serve`, and round-trip through the substrate backend — including native
`crypto.randomUUID()`. Toolchain notes: match the WIT version to the engine
(`wasi:http@0.2.3` here) and serve with `-S cli=y` (StarlingMonkey's stdio
feature imports `wasi:cli`).

**Streaming, pooling and bindings — ✅.** The backend now:
- **streams** responses (`ReverseProxy.FlushInterval = -1`); a `ReadableStream`
  component's chunks reach the client as produced (validated).
- **pools** processes per component: a configurable number of `wasmtime serve`
  instances per cid, round-robined, with dead-process respawn, idle eviction and
  an LRU cap (`TAUBYTE_COMPONENT_{POOL_SIZE,IDLE_TTL,MAX}`).
- exposes **bindings** on the handler's `env`. The substrate injects internal
  loopback headers — `x-taubyte-env` (JSON secrets/config) and `x-taubyte-bindings`
  (a per-website endpoint URL) — which the shim turns into `env.<SECRET>`,
  `env.KV` and `env.STORAGE` (fetch clients), then strips. Validated end to end:
  a secret arrives via the header and `env.KV.get` round-trips through an
  outbound fetch to the endpoint.

**Substrate-side KV/storage wiring — ✅.** The binding endpoint is backed by the
node's real services:
- `bindings` — the loopback HTTP server (`/{token}/kv/...`, `/{token}/storage/...`)
  over storage-agnostic `KV`/`Storage` interfaces, with random per-website tokens
  so one component can't reach another's data.
- `componentbindings` — adapters from Taubyte's `database.KV` / `storage.Storage`
  to those interfaces (handling `datastore.ErrNotFound` → miss, and versioned
  storage → latest-version get/put), plus `Enable(db, storage, opts)` which wires
  `website.RegisterComponentBindings`. The KV resource is selected by a matcher
  (default: the website name).
- The substrate node calls this from `attachNodes` via a build-tagged
  `attachComponentBindings()` — active only under `-tags wasmtime_component`, a
  no-op (and zero dependency) otherwise; the server is closed on shutdown.

Validated through the full real chain (a StarlingMonkey counter component's
`env.KV.put`/`get` → the loopback server → the real adapter → a database service):
the counter persists and increments across requests. Only the backing database
in that test is a faithful in-memory fake; the binding server, adapter, shim,
component and wasmtime backend are all real.

**Named bindings + secrets — ✅.** A website declares bindings in config
(`Website.Bindings`, threaded through `pkg/schema/website` and round-trip tested):
each maps a name to a `kv`/`storage` resource (by matcher) or a `secret`, and
surfaces as `env.<Name>` (Workers-style). The binding server, shim and injector
route by name (`x-taubyte-bindings` carries `{base, kv:[names], storage:[names]}`);
secrets resolve from the node environment (the binding's resource names the env
var, so values stay out of git). With no bindings declared, `env.KV` / `env.STORAGE`
default to resources matched by the website name (backwards compatible). The
full named-`env.KV` chain is validated end to end.

Remaining polish: a first-class secret *resource* type (today secrets come from
node env vars), and surfacing binding declaration in the console UI.

### 5. Next.js on the richer engine — ✅ (SSR + GET/POST APIs)
A real Next.js 14 App Router app, built with `next-on-pages` and run via
`--engine starlingmonkey`, works end to end: a dynamic React SSR page
(`react-dom/server`, `force-dynamic`) renders, prerendered pages serve from the
static layer, and `GET`/`POST` edge API routes work (`POST` echoes its JSON
body). Three shim polyfills bridge gaps in the StarlingMonkey build jco ships:
ReadableStream async iteration, byte-stream `tee()` (both for streaming SSR), and
`new Request(reqWithBody, init)` (the native clone-with-body traps with
`IndirectCallToNull`; reconstructed via URL + explicit body — this is what
unblocked `POST`).

These polyfills are a stopgap for the shipped engine; once WASI 0.2.10 lands in a
wasmtime release the newer StarlingMonkey (which implements them natively) drops
in and they no-op (see §4). Real apps may surface further per-app shims — the
pattern (capture the JS-level error with stdio on, polyfill the missing Web API)
is established.

### 6. Node HTTP-server frameworks + Bun — ✅ (Express 4, Koa 3, Bun.serve)
`taubyte-ssr-adapter --mode node` runs apps built on Node's HTTP server
(`http.createServer`/`app.listen()`) on the component tier. The core is a
`node:http` → `fetch` **bridge** (`runtime/node-modules/node-http.js`):
`createServer`/`listen()` capture the request handler (no socket is bound), and
each `wasi:http` request is adapted into a Node `IncomingMessage`/`ServerResponse`
pair, driven through the handler, and the response turned back into a `fetch`
`Response`. Around it sit self-contained shims for the node builtins an HTTP app
touches — `path`, `stream` (Readable/Writable/Transform on EventEmitter), `crypto`
(WebCrypto + sync SHA-1/SHA-256 for `etag`), `util`, `url`, `querystring`,
`string_decoder`, `events`, `buffer`, `assert`, with loud stubs for
`fs`/`net`/`zlib`/`v8`. npm deps resolve under esbuild `--platform=browser` (their
browser builds avoid node internals) and `process` is shaped as an edge runtime so
libraries stay on that path; injected secrets land in `process.env`.

Output is the same `component` artifact the `wasmtimehttp` backend already serves,
so this needed **no substrate changes**. Validated end to end under `wasmtime
serve`: **Express 4** (routing, headers, `express.json()` body parsing,
`res.send`/`res.json`, 404) and **Koa 3** (async `ctx` middleware +
`koa-bodyparser`).

Design notes worth keeping (each was a real bug found and fixed):
- **CJS shape matters.** Builtins Node exposes as a *callable/class module*
  (`events`→EventEmitter, `stream`→Stream, `assert`→the assert fn) must be authored
  as CommonJS so `require()` returns that value, not an ESM namespace; esbuild
  infers format from extension, so those ship as `.cjs`.
- **No field collisions with the ecosystem.** The request's raw bytes can't live on
  `req._body` — body-parser uses `req._body` as its "already parsed" flag (it's
  `req._rawBody` now).
- **Function constructors, not classes, for inherited shims.** iconv-lite does
  `StringDecoder.call(this, enc)`; an ES6 `class` throws "cannot be invoked without
  'new'", which stranded `express.json()`. `StringDecoder` is a function
  constructor.
- **Complete `Buffer` statics.** safe-buffer only re-exports a Buffer that has
  `from`/`alloc`/`allocUnsafe`/`allocUnsafeSlow`; missing ones make it wrap Buffer
  and drop non-enumerable statics like `isBuffer`.
- **Async handlers must surface errors.** Koa ends the response only after its
  middleware promise resolves; the bridge respects the handler's returned promise
  and rejects (→ 500) instead of hanging the event loop.

**Bun** (`--mode bun`) and **Deno** (`--mode deno`) reuse the same machinery:
`Bun.serve({ fetch })` / `Deno.serve(handler)` are Web-standard fetch handlers, so
a `Bun`/`Deno` global (installed before the app runs) captures the handler and the
bridge (`serveBun`/`serveDeno`) dispatches each request — alongside the node-builtin
shims (both are node-compatible) and the component shim's Web-API polyfills. Secrets
land in `process.env` (and thus `Bun.env`/`Deno.env`). `Bun.file`/`Deno.readFile`
and WebSocket upgrade aren't available. Both validated end to end (routing, JSON
body, env secret).

**Express 5** also works: path-to-regexp v8's `\p{ID_Start}` route regexes are
downleveled at build time (see §4's note on the engine's missing Unicode tables),
and body-parser 2.x / finalhandler drove three more shim fixes (enumerable Buffer
statics for safer-buffer, an EventEmitter-shaped `req.socket` for on-finished, and
`socket.readable`). **Vue 3 SSR** (`renderToString`) renders on the `fetch` tier —
the same path Nuxt/SolidStart/Astro/Angular-SSR take via their edge/node adapters.

**Fastify** is partial: it bundles and routes, but its async plugin loader (avvio)
doesn't complete boot on the engine — needs dedicated work. The full, honest
per-framework matrix (incl. why Vite and Jest/Mocha/Vitest/Cypress aren't hosting
targets) lives in `docs/framework-support.md`.

Known engine limit: routers on `path-to-regexp` v8+ (`@koa/router`, Express 5) use
`\p{…}` Unicode-property regexes the shipped StarlingMonkey lacks Unicode tables
for; Express-4-style routers / manual routing work today, and the newer engine
(§4) lifts it. Full Node/Bun (`fs`/`net`/`child_process`/native addons) remains out
of scope — route data through Taubyte primitives.

## Guiding constraint

A WASM sandbox has no ambient filesystem, sockets, or native addons. Data access
goes through Taubyte primitives (KV, storage, pubsub, functions), not raw
TCP/Postgres or `node:fs`. The target is **edge-runtime** parity, which is where
the modern framework ecosystem is converging anyway.
