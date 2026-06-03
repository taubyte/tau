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

## Hono / Next.js (needs a Web-API polyfill)

Frameworks built on Web standards (Hono's `app.fetch(Request) -> Response`,
Next, ...) cannot run on bare Javy because `Request`/`Response`/`URL`/`Headers`
are absent. To support them, bundle a polyfill that provides those globals and
adapt the framework's fetch handler to the JSON contract above, e.g.:

```js
import app from "./hono-app.js";
import "./web-polyfill.js"; // provides Request/Response/Headers/URL
export default async function handle(req) {
  const request = new Request("http://x" + req.url, { method: req.method, headers: req.headers, body: req.body || undefined });
  const res = await app.fetch(request);
  const headers = {}; res.headers.forEach((v, k) => (headers[k] = v));
  return { status: res.status, headers, body: await res.text() };
}
```

Providing/validating that polyfill is the remaining JS-side work; the platform
hosts the result unchanged.

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
