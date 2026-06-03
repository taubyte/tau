# Next.js on Taubyte — adapter design (in progress)

Goal: host **edge-runnable Next.js** on Taubyte — server-rendered pages, route
handlers, and middleware — backed by Taubyte's own KV/DB/storage. This is the
same subset Cloudflare Pages (`next-on-pages`) and OpenNext target.

**Ceiling (be honest):** a Next app that needs the full Node server
(`output: 'standalone'`'s `server.js`), native addons, or raw TCP (e.g. a direct
Postgres socket) cannot run in a WASM sandbox. Those must move to Taubyte
primitives or Web-standard/edge equivalents. The target is the **edge runtime**
shape: Web APIs (`Request`/`Response`/`fetch`/streams), not arbitrary Node.

## Pipeline

```
next build ─▶ .next/ ─┬─ translate manifests ─▶ Taubyte SSR manifest + routing   [DONE]
                      ├─ collect static (.next/static, public, prerendered html) ─▶ asset/   [next]
                      └─ bundle edge handler + middleware ──┐
                                                            ▼
                            Web-API + Node-compat polyfills ─▶ esbuild ─▶ Javy ─▶ handler.wasm.zip
                                                            │                        (wasi-stdio)
                                                            ▼
                                          Taubyte website asset (static + manifest + handler)
                                                            ▼
                                       substrate wasi-stdio serving  [DONE — proven]
```

## Components & status

| Stage | What it does | Status |
| --- | --- | --- |
| **Manifest translation** | `.next/{routes,prerender,middleware}-manifest.json` → Taubyte SSR manifest + a Report (prerendered / dynamic / api routes, middleware matchers, basePath). | ✅ `pkg/specs/builders/frameworks/nextjs` (tested) |
| **Asset assembly** | Copy `.next/static` → `/_next/static`, `public/` → `/`, pre-rendered HTML into the asset so the runtime's static-file check serves them. | ⬜ next (straightforward Go/script) |
| **Edge handler bundle** | Wrap Next's edge server output + middleware into one `fetch`-style handler bundled to the wasi-stdio contract. | ⬜ the core work |
| **Web-API layer** | `Request`/`Response`/`Headers`/`URL`/`fetch`/`TextEncoder`/streams polyfills (bare Javy lacks them). | ⬜ required by the handler |
| **Node-compat shims** | `fs` (read-only over the asset), `path`, `buffer`, `stream`, `process`, `async_hooks` — the subset Next's edge runtime touches. | ⬜ required by the handler |
| **Substrate serving** | Static + dynamic routing, `/api`, the wasi-stdio ABI that runs the handler. | ✅ proven end to end |

## Middleware

Next middleware runs in the edge runtime before routing. The translator already
extracts the matchers (`middleware-manifest.json`). In serving terms, any
middleware-matched path must reach the handler (it is, via the `/` SSR
catch-all), and the bundled handler runs middleware first, then the route. So
middleware needs **no new substrate behaviour** — it's handled inside the edge
handler bundle.

## Why the runtime layer is the hard part

Bare Javy/QuickJS is ES + `console` + `TextEncoder`/`TextDecoder` only. Next's
edge handler expects Web APIs and a slice of Node. Standing those up in wasm
(via polyfills on QuickJS, or by swapping in StarlingMonkey/SpiderMonkey which
ships Web APIs) is the multi-week core, and is validated by running real Next
output — not unit tests. The translator and serving path below it are done and
proven, so that work has a stable target to build against.

## Try the translator

```go
import "github.com/taubyte/tau/pkg/specs/builders/frameworks/nextjs"

manifest, report, err := nextjs.Translate("./my-next-app") // dir containing .next/
```

Run `next build` on any app, point `Translate` at it, and inspect the emitted
SSR manifest + routing report — that's the routing brain of the adapter, working
today against real builds.
