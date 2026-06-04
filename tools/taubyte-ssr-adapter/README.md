# taubyte-ssr-adapter (prototype)

Compiles a JavaScript request handler into a Taubyte server bundle the SSR
website runtime can host.

```
entry.js  +  shim  ──esbuild──▶  bundle.js  ──javy──▶  module.wasm  ──▶  handler.wasm.zip
```

## Handler contract (polyfill-free)

Bare Javy/QuickJS provides ES + `console` + `TextEncoder`/`TextDecoder` + its
own `Javy.IO`, but **no Web APIs** (no `fetch`/`Request`/`Response`/`URL`). So
the runnable-today contract is plain JSON objects — default-export a function:

```js
// app.js
export default function handle(req) {
  // req: { method, url, headers, body }
  if (req.url.startsWith("/api/")) {
    return { status: 200, headers: { "content-type": "application/json" },
             body: JSON.stringify({ ok: true, path: req.url }) };
  }
  return { status: 200, headers: { "content-type": "text/html" },
           body: "<h1>Hello from SSR</h1>" };
}
```

A complete example is in `example/app.js`.

## Usage

```sh
# Requires: esbuild (or npx) and javy on PATH.
# Use Javy >= 5.0: it enables the event loop (`build -J event-loop=y`), which
# async handlers need. On older/plugin-less Javy the adapter refuses `--mode
# fetch` (always async) with a clear error rather than shipping a module that
# traps at runtime on the first await. Override the invocation with
# TAUBYTE_JAVY_ARGS if your Javy enables the loop differently.
go run ./tools/taubyte-ssr-adapter \
  --entry ./app.js \
  --framework js \
  --out   ./build/__taubyte__/handler.wasm.zip \
  --manifest ./build/__taubyte__/ssr.json
```

Then zip your static output together with `__taubyte__/handler.wasm.zip` and
`__taubyte__/ssr.json` into the website `build.zip` — the runtime serves static
files directly and routes everything else to the bundle.

## Web-standard frameworks (Hono, Remix, SvelteKit) — `--mode fetch`

Frameworks built on Web standards export `app.fetch(Request) -> Response`. Bare
Javy lacks `Request`/`Response`/`URL`/`Headers`, so `--mode fetch` injects the
Web API polyfill (`runtime/web.js`) before the app and dispatches through it:

```sh
npm i hono
go run ./tools/taubyte-ssr-adapter --mode fetch --framework hono \
  --entry ./tools/taubyte-ssr-adapter/example/hono-app.js \
  --out /tmp/handler.wasm.zip --manifest /tmp/ssr.json
```

`example/hono-app.js` is a runnable Hono app. The polyfill targets the common
SSR path (methods, headers, text/json bodies, URL parsing, FormData, Cache API,
`cloudflare:workers`), not full WHATWG conformance — outbound `fetch` and streams
are stubbed.

A real SvelteKit 5 app (built with `@sveltejs/adapter-cloudflare`) runs through
this pipeline end to end — SSR page render with a `+page.server.js` `load()`,
`/api/*` server routes (GET/POST with `request.json()`), and the framework's own
404 page all return correctly:

```sh
# in your SvelteKit project, after `npm run build`:
go run /path/to/tau/tools/taubyte-ssr-adapter --mode fetch --node \
  --framework sveltekit \
  --entry .svelte-kit/cloudflare/_worker.js --out /tmp/sk.zip
unzip -o /tmp/sk.zip main.wasm -d /tmp
# Test a DYNAMIC route (a prerendered "/" is served by the static layer, not the
# bundle — standalone it delegates to env.ASSETS and 404s; see --site below):
echo '{"method":"GET","url":"/api/echo"}' | wasmtime /tmp/main.wasm
```

### Assembling a deployable site — `--site`

The handler bundle only runs *dynamic* routes. A real site also has static and
prerendered files (SvelteKit emits `index.html`, `404.html`, `_app/…` into
`.svelte-kit/cloudflare`), which Taubyte's static layer serves directly — so a
prerendered `/` never reaches the bundle. `--site` assembles both halves into one
deployable website `build.zip` (static/prerendered assets at the root + the
handler and manifest under `__taubyte__/`, with edge control files like
`_worker.js`/`_routes.json` excluded):

```sh
go run /path/to/tau/tools/taubyte-ssr-adapter --mode fetch --node \
  --framework sveltekit \
  --entry .svelte-kit/cloudflare/_worker.js \
  --site  .svelte-kit/cloudflare \
  --out   build.zip
```

Upload that `build.zip` as the website asset: the substrate serves prerendered
pages and `_app/` statically and routes `/api/*` and other dynamic paths to the
bundle. Flat prerenders (`about.html`) are also written in clean-URL form
(`about/index.html`) so `/about` resolves.

`--site` also embeds the text-like assets (HTML/CSS/JS/JSON/SVG, each under
`--asset-embed-max`, default 100 KiB) into the bundle so `env.ASSETS` resolves
them in-process — a one-shot wasi-stdio bundle can't call back to the host, so
this is how a standalone bundle serves its own prerendered pages and how
SvelteKit's `read()` of a server asset gets real bytes. Large/binary assets are
left to the static layer; streaming/`response.body` reads of those remain a gap.

### Next.js (`@cloudflare/next-on-pages`)

next-on-pages emits a multi-module worker: `_worker.js/index.js` plus per-route
`__next-on-pages-dist__/functions/*.func.js` it pulls in via dynamic
`import(runtimeStringPath)`. Javy is a single module with no runtime module
loader, so the adapter detects this layout and folds it into one bundle: it
statically imports every route/cache module into a registry, rewrites the
worker's dynamic imports to a registry lookup, pre-installs next-on-pages'
route-isolation, and shims `node:buffer`/`AsyncLocalStorage` on the global so
the route modules find them at evaluation time. Just point `--entry` at the
generated `index.js`:

```sh
npx @cloudflare/next-on-pages
go run /path/to/tau/tools/taubyte-ssr-adapter --mode fetch --node --framework nextjs \
  --entry .vercel/output/static/_worker.js/index.js \
  --site  .vercel/output/static --out build.zip
```

Validated against a Next.js 14 App Router app:
- **On the Javy tier:** prerendered pages serve from the static layer and `GET`
  edge `route.js` handlers work, but a dynamic React **SSR** page crashes
  Javy/QuickJS's bytecode compiler ("stack underflow") — heavy React SSR needs
  the StarlingMonkey engine below.
- **On `--engine starlingmonkey`:** a dynamic React SSR page (`react-dom/server`,
  `runtime='edge'`, `force-dynamic`) **renders end to end** (full HTML, server
  timestamp, hydration scripts), prerendered pages serve from the static layer,
  and `GET` **and** `POST` `/api/*` edge routes work (`POST` echoes its JSON
  body). This needs the three shim stream/request polyfills below.

The older manifest-translation path is the `taubyte-next-adapter` (see
`docs/nextjs-adapter.md`).

## StarlingMonkey engine — `--engine starlingmonkey`

The Javy/QuickJS tier is small and polyfilled, and its compiler chokes on heavy
bundles (React SSR). `--engine starlingmonkey` instead targets **StarlingMonkey**
(SpiderMonkey on WASM): a real JS engine with native Web APIs (URL, streams,
SubtleCrypto, `fetch`) that runs the React `renderToString` that crashes QuickJS.

It bundles the app (no Web-API polyfill — the engine is native), wraps it in a
fetch-event shim, and runs `jco componentize` against the vendored
`wasi:http/proxy` WIT to emit a **WebAssembly Component** (`abi:"component"` in
the manifest):

```sh
# requires esbuild + jco (@bytecodealliance/jco) on PATH. ./app.js is YOUR entry
# (a Web-standard fetch handler); --site is optional (add it only to bundle a
# built static-asset dir into a full build.zip).
go run ./tools/taubyte-ssr-adapter --mode fetch --engine starlingmonkey \
  --framework hono --entry ./app.js --out handler.component.wasm
```

The component is **not** a wasi-stdio module — neither wazero nor the wasmtime-go
bindings host the Component Model. The substrate serves it via the opt-in
`wasmtimehttp` backend (build tag `wasmtime_component`), which shells out to
`wasmtime serve` (the full `wasi:http` host) and reverse-proxies to it. Build the
substrate with that tag and `wasmtime` on PATH to enable the `component` ABI;
otherwise `component` assets fail fast and the Javy tier is unaffected.

The backend **streams** responses (a `ReadableStream` reaches the client as
produced), **pools** `wasmtime serve` processes per component (round-robin +
respawn + idle eviction + LRU cap, tunable via
`TAUBYTE_COMPONENT_{POOL_SIZE,IDLE_TTL,MAX}`), and surfaces **named bindings** on
the handler's `env` (Workers-style). A website declares its bindings in config —
each maps a name to a `kv`/`storage` resource (by matcher) or a `secret` — and
they become `env.<Name>`: KV (`get`/`put`/`delete`/`list`), storage (`get`/`put`),
or a secret value. With none declared, `env.KV` / `env.STORAGE` are provided by
default (resources matched by the website name). Secrets resolve from the node's
environment (the binding's resource names the env var), so they stay out of git.
See `example/bindings.js`.

The fetch-event shim also installs three **compatibility polyfills** for gaps in
the StarlingMonkey build jco currently ships (each no-ops on an engine that has
the feature): ReadableStream **async iteration** (`for await (chunk of stream)`),
byte-stream **`tee()`**, and **`new Request(reqWithBody, init)`** (the native
clone-with-body path traps with `IndirectCallToNull`, so it's reconstructed via
the request's URL + explicit body). Next.js App Router exercises all three — SSR
needs the first two (else the render stream is "not iterable" / un-tee-able and
the body is empty), and the worker's per-request re-wrap needs the third (else
`POST` traps).

Validated end to end: a fetch handler, a **dynamic Next.js React SSR page**, a
streaming `ReadableStream` response, and a named `env.KV` round-trip (component →
loopback server → real Taubyte database adapter) all work through the backend
(with native
`crypto.randomUUID()`). See `docs/js-runtime-roadmap.md`.

## Node HTTP-server frameworks (Express, Koa, …) — `--mode node`

`--mode node` runs an app built on Node's HTTP server — `http.createServer((req,
res) => …)`, or a framework that wraps it (`app.listen()` in Express/Koa/Connect/
Fastify). It implies `--engine starlingmonkey` (the component tier provides the
`fetch`/Web-API host the bridge runs on).

```sh
# requires esbuild + jco on PATH. ./app.js is YOUR Node app (calls app.listen()
# or default-exports a server / request handler).
go run ./tools/taubyte-ssr-adapter --mode node --engine starlingmonkey \
  --framework express --entry ./app.js --out handler.component.wasm
```

How it works: `node:http` is aliased to a bridge (`runtime/node-modules/node-http.js`)
whose `createServer`/`listen()` **capture** the request handler instead of binding
a socket; each incoming `wasi:http` request is adapted into a Node
`IncomingMessage`/`ServerResponse` pair and driven through it, and the response is
turned back into a `fetch` `Response`. The rest of the Node builtins an HTTP app
reaches for are provided as self-contained shims (`path`, `stream`, `crypto` with
sync SHA-1/SHA-256, `util`, `url`, `querystring`, `string_decoder`, `events`,
`buffer`, `assert`, plus loud stubs for `fs`/`net`/`zlib`/`v8` — there is no
filesystem or raw socket here). npm dependencies resolve under esbuild's
`--platform=browser` (their browser builds avoid node internals), and `process` is
shaped like an edge runtime so libraries stay on that path. Secrets injected by
the substrate (`x-taubyte-env`) are merged into `process.env`.

This is **HTTP request-handler compatibility, not a full Node runtime**: no
ambient `fs`/`net`/`child_process`/native addons — route data through Taubyte
primitives (KV/storage). Validated end to end against **Express 4** (routing,
`req.headers`, `express.json()` body parsing, `res.send`/`res.json`, 404s) and
**Koa 3** (`ctx`/async middleware + `koa-bodyparser`), both served under `wasmtime
serve` and producing the `component` ABI the `wasmtimehttp` backend already hosts —
so `--mode node` needs no substrate changes. One known engine limit: routers built
on `path-to-regexp` v8+ (e.g. `@koa/router`, Express 5) use `\p{…}` Unicode-property
regexes the shipped StarlingMonkey lacks Unicode tables for; manual routing or
Express-4-style routers work, and the newer engine (see roadmap §4) lifts it.

## Bun apps (`Bun.serve`) — `--mode bun`

`--mode bun` runs a Bun app whose HTTP entrypoint is `Bun.serve({ fetch(request,
server) -> Response })`. Bun's handler is a Web-standard fetch handler, so it maps
directly onto the component tier (also implies `--engine starlingmonkey`).

```sh
go run ./tools/taubyte-ssr-adapter --mode bun --engine starlingmonkey \
  --framework bun --entry ./app.js --out handler.component.wasm
```

A `Bun` global (also importable as `bun`) is installed before the app runs; its
`serve()` captures the fetch handler instead of binding a socket, and the bridge
drives each `wasi:http` request through it (reusing the node-builtin shims, since
Bun is node-compatible, plus the component shim's Web-API polyfills). Config and
secrets arrive through `process.env` / `Bun.env`. Validated end to end (routing,
JSON body, 404, and an injected secret read via `Bun.env`). `Bun.file`
(filesystem) and WebSocket `upgrade` are not available — route data through
Taubyte primitives.

## Why Javy + WASI stdio

Javy embeds QuickJS and exposes I/O over **WASI stdin/stdout** — calling host
functions would require a custom Javy plugin. So the bundle reads the request
from stdin and writes the response to stdout:

```
stdin  : {"method","url","headers":{},"body"}
stdout : {"status","headers":{},"body"}
```

`shim/shim.js` performs that bridge: it parses the request, calls the handler,
and serializes the response.

## Runtime support

The manifest this tool emits sets `"abi": "wasi-stdio"`. The substrate SSR
runtime supports two handler ABIs:

- **function** (default) — exported `ssrHandler(eventId)` + go-sdk host calls;
  what a hand-written/TinyGo handler uses.
- **wasi-stdio** — per request, the runtime writes the serialized request to the
  module's stdin, runs it, and reads the response from stdout. This is what Javy
  bundles use, and what this tool targets.

The stdio request/response envelopes match `shim/shim.js`:

```
stdin  : {"method","url","headers":{},"body"}
stdout : {"status","headers":{},"body"}
```

The wasi-stdio path is exercised end to end (without Javy) by
`services/monkey/fixtures/compile/website_ssr_stdio_test.go` using a plain WASI
command module; a Javy bundle is hosted identically.

## Status

- ✅ Substrate `wasi-stdio` ABI — implemented (`core/vm` stdin + the website
  serving path), proven end to end with a WASI command module.
- ✅ Packaging (`handler.wasm.zip`) and manifest emission — unit-tested,
  round-trips through the runtime's manifest parser.
- ✅ `esbuild`/`javy` pipeline and `shim/shim.js` — runs end to end with the
  toolchain installed (esbuild + Javy >= 5.0 + a WASI runtime). Async handlers
  need Javy's event loop; the adapter enables it and refuses `--mode fetch` with
  a clear error on a Javy that can't (rather than shipping a trap-at-runtime
  module).
- ✅ Hono + SvelteKit (`adapter-cloudflare`) — render through the Web-API
  polyfill + Workers shims (`web.js`, `node.js`, `cloudflare:workers`, Cache
  API). SvelteKit SSR + `/api` routes + 404 validated against a real app.
- ⚠️ Next.js edge handler — same shape; builds on this polyfill plus the
  `taubyte-next-adapter` (see `docs/nextjs-adapter.md`). Validate per app.
- ✅ Node HTTP-server frameworks via `--mode node` (`--engine starlingmonkey`) —
  a `node:http` → fetch bridge + node-builtin shims. **Express 4 & 5** (incl.
  `express.json()` and path-to-regexp v8 params) and **Koa 3** (+ `koa-bodyparser`,
  `@koa/router`) validated end to end. Not a full Node runtime (no
  fs/net/child_process); HTTP request-handling only.
- ✅ Bun apps via `--mode bun` and Deno apps via `--mode deno` — a
  `Bun.serve({fetch})` / `Deno.serve(handler)` global dispatched on the component
  tier. Routing + JSON body + `Bun.env`/`Deno.env` secret injection validated.
- ✅ SSR meta-frameworks on `--mode fetch` — **Vue 3** (`renderToString`) validated;
  Next.js, Nuxt, SolidStart, Astro take the same fetch-handler path.
- ◑ Fastify / NestJS / Apollo Server / Angular SSR — see
  [`docs/framework-support.md`](../../docs/framework-support.md) for the full,
  honest per-framework matrix (incl. Fastify's avvio-boot gap, and why Vite and
  test runners aren't hosting targets).
- ⬜ Streams (`ReadableStream`/`TransformStream`) and outbound `fetch` — stubbed;
  wire to Taubyte primitives next.
