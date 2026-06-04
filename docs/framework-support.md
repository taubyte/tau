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
| **Bun** (`Bun.serve`) | `bun` | routing, JSON body, `Bun.env` secret injection |
| **Deno** (`Deno.serve`) | `deno` | routing, JSON body, `Deno.env` secret injection |
| **Vue 3 SSR** (`vue/server-renderer`) | `fetch --node` | `renderToString` renders server-side |
| **Hono** | `fetch` | (earlier) Web-standard fetch app |
| **Next.js 14** (App Router via next-on-pages) | `fetch --node` | dynamic React SSR + GET/POST edge routes |
| raw `node:http` | `node` | `createServer`/`listen`, `req.on('data')`, `res.writeHead/end` |

## ◑ Runs on a validated tier — validate per app

These layer on a shape that's validated; expect them to work, with per-app shims
for whatever their bundle happens to call. Heavier ones may surface more node
builtins (the error names the missing one; adding a shim is mechanical).

| Framework | how | tier |
|-----------|-----|------|
| **NestJS** | `@nestjs/platform-express` adapter (Express works) | `node` |
| **Apollo Server** | Express integration or `startStandaloneServer` (node http) | `node` |
| **Nuxt 3** | Nitro `cloudflare-module` / node preset → a fetch/node handler | `fetch`/`node` |
| **SolidStart** | Cloudflare/`nitro` preset → a fetch handler | `fetch` |
| **Astro** | `@astrojs/cloudflare` (fetch) or `@astrojs/node` | `fetch`/`node` |
| **Angular SSR** | `@angular/ssr` runs on an Express server | `node` |
| **SvelteKit / Remix / SolidJS** | `adapter-cloudflare` style fetch handler | `fetch` |
| **Vue / React / Solid / Svelte** (bare SSR) | a fetch handler around `renderToString` | `fetch` |

The pattern for SSR frameworks is always the same as the validated Vue/Next/React
path: build the framework for its **edge/Cloudflare** adapter (emits a Web-standard
fetch handler) and run `--mode fetch --node`, or its **node** adapter and run
`--mode node`.

## ⚠️ Partial

| Framework | state |
|-----------|-------|
| **Fastify** | Bundles and routes, but its async plugin loader (**avvio**) fails to complete boot on the engine (route contexts end up uninitialized). Needs dedicated work on avvio's boot lifecycle / `process.nextTick` ordering. Anything on the **Fastify adapter** (Nest-fastify, some Apollo setups) inherits this until it's fixed. |

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
