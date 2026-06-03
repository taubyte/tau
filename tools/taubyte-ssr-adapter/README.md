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

Validated against a Next.js 14 App Router app: **edge routes** (`route.js` /
pages with `runtime = 'edge'`) compile and run — `GET`/`POST /api/*` return
correctly — and prerendered pages serve from the static layer. **Known wall:**
a React **SSR** page (`react-dom/server`) is a ~700 KB module that crashes
Javy/QuickJS's bytecode compiler ("stack underflow"); heavy React SSR needs the
StarlingMonkey engine below. Use `runtime='edge'` + prerendering on the Javy
tier; for dynamic React SSR use `--engine starlingmonkey`. The older
manifest-translation path is the `taubyte-next-adapter` (see
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
# requires esbuild + jco (@bytecodealliance/jco) on PATH
go run ./tools/taubyte-ssr-adapter --mode fetch --engine starlingmonkey \
  --framework hono --entry ./app.js --out handler.component.wasm
```

The component is **not** a wasi-stdio module — neither wazero nor the wasmtime-go
bindings host the Component Model. The substrate serves it via the opt-in
`wasmtimehttp` backend (build tag `wasmtime_component`), which shells out to
`wasmtime serve` (the full `wasi:http` host) and reverse-proxies to it. Build the
substrate with that tag and `wasmtime` on PATH to enable the `component` ABI;
otherwise `component` assets fail fast and the Javy tier is unaffected.

Validated end to end: a fetch handler and a React SSR page both componentize,
serve under `wasmtime serve`, and round-trip through the backend (with native
`crypto.randomUUID()`). See `docs/js-runtime-roadmap.md`.

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
- ⬜ Streams (`ReadableStream`/`TransformStream`) and outbound `fetch` — stubbed;
  wire to Taubyte primitives next.
