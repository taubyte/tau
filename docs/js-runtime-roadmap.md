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

### 3. Streaming ABI — ⬜
The `wasi-stdio` ABI is request → **full** response (buffered). RSC streaming and
large/`ReadableStream` bodies need a streaming path. Design:
- Substrate: instead of reading all of stdout then writing, stream stdout → the
  HTTP response as it's produced (chunked). Add `vm.Config` stdout streaming and
  a `serveSSRStream` that copies incrementally.
- Manifest: a `stream: true` hint (or infer from a `Transfer-Encoding` in the
  response envelope's headers preamble).
- Independent of the engine; benefits Javy and component engines alike.

### 4. Engine swap → component-model JS engine (StarlingMonkey) — ⬜ (the big one)
Javy/QuickJS is a small engine with a thin Web-API surface we polyfill by hand.
Real Next.js (and rich frameworks) want a near-browser surface: full WebCrypto,
streams, `fetch`, `AsyncLocalStorage`, etc. **StarlingMonkey** (SpiderMonkey on
WASM, used by Fastly Compute) provides that.

**The core obstacle:** StarlingMonkey is a **WebAssembly Component** (Component
Model + WASI Preview 2, `wasi:http`). Taubyte's VM is **wazero**, which runs
**core modules + WASI Preview 1** and does **not** support the Component Model.
So the engine can't just be dropped in.

Options:
- **A — add a component-model backend to the VM layer.** Introduce a second
  runtime (e.g. `wasmtime-go`) behind a `vm.Engine` interface; route
  `ABIComponent` bundles to it via `wasi:http`. Largest surface area (a new
  dependency + host wiring), but the canonical path and reuses upstream
  StarlingMonkey unchanged.
- **B — a WASI-P1 build of the engine.** Build SpiderMonkey/StarlingMonkey (or a
  QuickJS superset like `wasmedge-quickjs`) as a P1 core module wazero can run,
  with a stdio/host bridge. Keeps wazero; heavy engine-build work, less upstream
  support.
- **C — pre-initialize with `weval`/wizer.** Orthogonal optimization (cold-start),
  still wasmtime-oriented.

**Recommendation:** Option A. The seam is already in place and compile-checked:

- `serveSSR` dispatches through the `ssrEngines` registry (step 2).
- `ABIComponent` has a concrete contract — the `ComponentRuntime` interface in
  `services/substrate/components/http/website/component.go`:
  `ServeHTTP(ctx, key, component []byte, w, r, limits)` + `Name()`.
- A backend lives in its own package (so the wasmtime dependency stays out of the
  substrate core) and enables itself with `website.RegisterComponentRuntime(...)`
  from `init()`. Until then, `component` assets fail fast.

So building the engine is: implement `ComponentRuntime` over `wasmtime-go` with
the Component Model + `wasi:http`, embed/build StarlingMonkey, and register it.
No changes to the static / function / Javy paths. This is the part that needs a
wasm/Rust/wasmtime environment to build and verify.

This is a platform-level effort (new runtime dependency, WASI-P2/`wasi:http`
host, build pipeline for the engine) — designed here, built in an environment
with the wasm/Rust/wasmtime toolchain.

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
