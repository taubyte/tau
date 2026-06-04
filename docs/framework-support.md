# JS framework support on Taubyte

Status of popular JS frameworks/runtimes on the `taubyte-ssr-adapter` →
StarlingMonkey component tier. Honest about what's been run end to end vs. what's
supported by virtue of running on a validated tier vs. what isn't a hosting
target at all.

The adapter has four runtime "shapes" (`--mode`):

| mode | entry shape | tier |
|------|-------------|------|
| `fetch` | `export default { fetch(Request) }` (Web standard) | component (StarlingMonkey) |
| `node` | `http.createServer` / `app.listen()` | component, via a node:http→fetch bridge |
| `bun` | `Bun.serve({ fetch })` | component, via a `Bun` global |
| `deno` | `Deno.serve(handler)` | component, via a `Deno` global |

All four emit the same `component` artifact the substrate's `wasmtimehttp` backend
already serves, so none need substrate changes. Serve locally with
`wasmtime serve -S cli=y handler.component.wasm`.

## ✅ Validated end to end

Run through the adapter + `wasmtime serve`, routes exercised (GET/POST/params/body/404):

| Framework | mode | notes |
|-----------|------|-------|
| **Express 4** | `node` | routing, headers, `express.json()`, `res.json`, 404 |
| **Express 5** | `node` | adds path-to-regexp v8 params (regex-downlevel) + body-parser 2.x |
| **Koa 3** | `node` | async `ctx` middleware, `koa-bodyparser`; `@koa/router` works (downlevel) |
| **Fastify 5** | `node` | routing, params, `req.body` JSON; async avvio boot deferred to first request (see below) |
| **NestJS 11** (Express adapter) | `node` | DI (reflect-metadata), routing, `@Body()` JSON, 404 — serverless-style lazy init (see below) |
| **Apollo Server 5** (GraphQL) | `node` | queries, args, validation errors — Express integration, serverless-style lazy init |
| **Bun** (`Bun.serve`) | `bun` | routing, JSON body, `Bun.env` secret injection |
| **Deno** (`Deno.serve`) | `deno` | routing, JSON body, `Deno.env` secret injection |
| **Vue 3 SSR** (`vue/server-renderer`) | `fetch --node` | `renderToString` renders server-side |
| **Nuxt 3** (Nitro `cloudflare-module`) | `fetch --node` | full SSR pages render (per-route HTML + hydration payload); see below |
| **Hono** | `fetch` | (earlier) Web-standard fetch app |
| **Next.js 14** (App Router via next-on-pages) | `fetch --node` | dynamic React SSR + GET/POST edge routes |
| raw `node:http` | `node` | `createServer`/`listen`, `req.on('data')`, `res.writeHead/end` |

## ◑ Runs on a validated tier — validate per app

These layer on a shape that's validated; expect them to work, with per-app shims
for whatever their bundle happens to call. Heavier ones may surface more node
builtins (the error names the missing one; adding a shim is mechanical).

| Framework | how | tier |
|-----------|-----|------|
| **SolidStart** | Cloudflare/`nitro` preset → a fetch handler | `fetch` |
| **Astro** | `@astrojs/cloudflare` (fetch) or `@astrojs/node` | `fetch`/`node` |
| **Angular SSR** | `@angular/ssr` runs on an Express server | `node` |
| **SvelteKit / Remix / SolidJS** | `adapter-cloudflare` style fetch handler | `fetch` |
| **Vue / React / Solid / Svelte** (bare SSR) | a fetch handler around `renderToString` | `fetch` |

The pattern for SSR frameworks is always the same as the validated Vue/Next/React
path: build the framework for its **edge/Cloudflare** adapter (emits a Web-standard
fetch handler) and run `--mode fetch --node`, or its **node** adapter and run
`--mode node`.

## Fastify — ✅ works, via deferred boot (the interesting case)

**Fastify** works end to end (routing, params, `req.body` JSON parsing, 404), but
getting there required solving a real architectural mismatch worth recording.

Fastify's plugin loader **avvio** boots **asynchronously**, kicked off at module
top-level (`app.listen()` → `app.ready()`). The component producer
(`componentize-js`) snapshots the top-level with **Wizer**, which (a) forbids the
platform timer and `wasi:random` during init, and (b) — the real blocker — does
**not preserve the pending JS job queue** across the snapshot. So an init-time
async boot's continuations are dropped and `app.ready()` never resolves (verified:
it stayed `"booting"`, and pumping the event loop at request time couldn't revive
it — the continuations were gone).

The fix has two parts, both in the adapter:
1. **Defer the boot to the first request.** When the app imports Fastify, the
   adapter aliases it to a thin wrapper (importing the real Fastify by absolute
   path) that intercepts `app.listen()` so it does **not** boot at init — Fastify
   creates the http server + route handler in its *constructor*, so those are
   captured regardless — and registers `app.ready()` on a deferred-ready list. The
   request bridge drives that list **once, on the first request**, in the real
   event loop where timers/random/continuations all work, so the boot completes.
2. **`AsyncResource.emitDestroy`** — Fastify wraps each request in an
   `async_hooks` `AsyncResource` and calls `emitDestroy()` on completion; the shim
   was missing it (now a no-op, with the other AsyncResource methods).

This generalizes: any framework that boots async at init can register on
`globalThis.__TAUBYTE_DEFER_READY` to be driven at first request.

The init-phase fallbacks added along the way are general wins too (they let *any*
app touch timers/random/UUIDs at module top-level without trapping Wizer):
`setTimeout`/`setInterval` fall back to a microtask (or drop, for long delays) when
the platform timer is unavailable at init; `crypto.getRandomValues`/`randomUUID`
use a non-secure PRNG during init and the real WebCrypto once serving (gated on a
"request phase" flag the bridges set); `Math.random` is pure-JS.

## NestJS — ✅ works (Express adapter, serverless-style)

**NestJS 11** works end to end (DI via `reflect-metadata`, routing, `@Body()` JSON
parsing, 404) on the `@nestjs/platform-express` adapter. Two things to know:

- **Build with `tsc` (or SWC), not esbuild alone.** Nest's DI resolves providers by
  constructor *type*, which needs `emitDecoratorMetadata` — esbuild doesn't emit it.
  Compile the app with `tsc` (`experimentalDecorators` + `emitDecoratorMetadata`),
  then run the emitted JS through the adapter (`--mode node`).
- **Use the serverless lazy-init shape** (same as Nest on Lambda/Vercel): don't
  `app.listen()`. Create the Nest app over an `ExpressAdapter(server)` and
  `app.init()` **lazily on the first request**, exporting a handler that uses the
  Express instance. This sidesteps the eager-async-boot/Wizer issue (the boot runs
  at request time) without any framework-specific adapter hook:

  ```ts
  const server = express();
  let booting; const ensure = () => booting ||= (async () => {
    const app = await NestFactory.create(AppModule, new ExpressAdapter(server));
    await app.init();
  })();
  export default async (req, res) => { await ensure(); server(req, res); };
  ```

Nest lazy-`require()`s optional peers (`@nestjs/microservices`, `@nestjs/websockets`,
`class-transformer/validator`); mark those `--external` via `TAUBYTE_ESBUILD_ARGS`
so the bundle resolves and Nest treats them as absent. Enabling shims added here:
`node:perf_hooks`/`node:repl`, and an **`Intl` stub** (this StarlingMonkey build
ships without the Intl/ICU API; the stub is locale-naive but lets Nest's deps
load). Nest-fastify would instead ride the Fastify path above.

## Nuxt 3 — ✅ SSR validated (the meta-framework path)

Nuxt 3 server-side rendering works end to end (validated: `/`, `/products`, `/a/b/c`
all render the right per-route HTML + the `window.__NUXT__` hydration payload), and
it needed **no adapter code changes** — just the right Nitro preset + the existing
`TAUBYTE_ESBUILD_ARGS` escape hatch. Build for the **Cloudflare** preset (Nitro
emits a single self-contained `export default { fetch }` worker — the canonical
"Nuxt on edge" output, and a perfect fit for the component tier):

```sh
# nuxt.config.ts:  nitro: { preset: "cloudflare-module" }
nuxt build                                   # -> .output/server/index.mjs (self-contained worker)
echo 'export default "{}";' > stub.js        # stub Cloudflare's __STATIC_CONTENT_MANIFEST virtual
TAUBYTE_ESBUILD_ARGS="--alias:__STATIC_CONTENT_MANIFEST=$PWD/stub.js" \
go run ./tools/taubyte-ssr-adapter --mode fetch --node --engine starlingmonkey \
  --framework nuxt --entry .output/server/index.mjs --out nuxt.wasm
wasmtime serve -S cli=y nuxt.wasm
```

Why Cloudflare and not the `node-server` preset: `node-server` leaves `vue`/`@vue/*`
as externals it traced as **node** builds, which clash with the adapter's
`--platform=browser` resolution; the Cloudflare preset inlines everything (unenv
node polyfills included) into one worker, so there's nothing to resolve and it's a
clean Web-standard fetch handler. The SSR **HTML** renders; the `/_nuxt/*` static
assets are served by Taubyte's static layer (deploy `.output/public` there), not
the worker — same split as Cloudflare Pages. **SolidStart / Astro / Analog** take
the identical path via their own Cloudflare/Nitro adapters.

## ❌ Not hosting targets

These aren't things you *host* on Taubyte (or any edge) — they run at build/CI
time, so "supporting them on the runtime" doesn't apply:

| Tool | what it is | what actually ships to Taubyte |
|------|-----------|--------------------------------|
| **Vite** | build tool / dev server | its **output**: static assets (Taubyte static hosting) + an optional SSR fetch handler (the `fetch` tier). Nothing to "run" — you deploy the build. |
| **Jest** | test runner | runs in CI, not hosted. Use it to test your app before `deploy`. |
| **Mocha** | test runner | same |
| **Vitest** | test runner (Vite-native) | same |
| **Cypress** | end-to-end browser test runner | same — drives a browser in CI against a deployed/preview URL |

If you want these wired into a Taubyte web session (run tests in CI on push), that's
a CI/setup concern, not a runtime-hosting one — happy to set that up separately.

## Known engine limit (affects several of the above)

The shipped StarlingMonkey (componentize-js 0.19.3) was built without Unicode
regex-property tables, so `\p{…}` regex escapes don't parse natively. The adapter
**downlevels** them at build time (Babel `transform-unicode-property-regex`), which
is what makes path-to-regexp v8 (Express 5, `@koa/router`) work. A newer
StarlingMonkey with full Unicode (once a wasmtime release carries WASI 0.2.10)
removes the need for the downlevel — see `docs/js-runtime-roadmap.md` §4.

## Guiding constraint

A WASM sandbox has no ambient filesystem, raw sockets, or native addons. Data
access goes through Taubyte primitives (KV, storage, pubsub, functions), not
`node:fs`/raw TCP/`Deno.readFile`. The target is **edge-runtime parity**, where the
modern framework ecosystem is already converging.
