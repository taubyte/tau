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
SSR path (methods, headers, text/json bodies, URL parsing), not full WHATWG
conformance — outbound `fetch` and streams are stubbed. **Status: prototype —
validate + iterate** against real apps via `javy` (the first milestone is a Hono
"hello world" rendering).

Next.js's edge handler is the same shape (it expects Web APIs); it builds on this
polyfill plus Node-compat shims and the `taubyte-next-adapter` (see
`docs/nextjs-adapter.md`).

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
- ⚠️ `esbuild`/`javy` pipeline and `shim/shim.js` — prototype for the
  polyfill-free JSON handler; validate with the toolchain installed (async
  handling depends on the Javy version draining the QuickJS job queue).
- ⬜ Hono/Next — needs the Web-API polyfill layer described above.
