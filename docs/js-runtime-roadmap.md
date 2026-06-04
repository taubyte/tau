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
- ◑ Next.js: static/pre-rendered today; edge dynamic via `next-on-pages` is
  experimental and limited by Javy-tier API coverage.
- ❌ Node http-server frameworks (Express/Koa/Fastify/Nest): out of scope (need
  a full Node runtime).

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

### 5. Next.js on the richer engine — ⬜
With a component engine + streaming, feed `next-on-pages` (or OpenNext) edge
output through `ABIComponent`. `taubyte-next-adapter` already maps routes,
assets and pre-rendered pages; the gap closes when the runtime covers the edge
API surface.

## Guiding constraint

A WASM sandbox has no ambient filesystem, sockets, or native addons. Data access
goes through Taubyte primitives (KV, storage, pubsub, functions), not raw
TCP/Postgres or `node:fs`. The target is **edge-runtime** parity, which is where
the modern framework ecosystem is converging anyway.
