# Server-Side Rendering & JavaScript Framework Hosting

Tau hosts websites by serving a build asset (a zip) from its content-addressed
storage. Historically that asset could only contain **static** files. This
document describes the extension that lets Tau host **server-side rendered**
(SSR) applications and the popular JavaScript frameworks built on top of them —
Next.js, Nuxt, SvelteKit, Remix, SolidStart, Astro, Vite, Create-React-App,
Vue, Angular, Express, Fastify, NestJS and friends — including their `/api`
routes.

## How it works

A website remains a single resource backed by a single build asset. What
changes is that the asset can now be **self describing**: when its build zip
contains an SSR manifest (`__taubyte__/ssr.json`), the substrate runtime serves
it as an SSR site instead of a static one.

```
build.zip
├── index.html                      ← static assets, served directly
├── _next/static/…                  ← immutable assets, served directly
├── favicon.ico
└── __taubyte__/
    ├── ssr.json                    ← the SSR manifest (routing + config)
    └── handler.wasm.zip            ← the server bundle, compiled to WebAssembly
```

At serve time the website serviceable:

1. **Serves real files straight from the asset.** Any request that resolves to
   an existing file in the zip (including a directory's `index.html`) is served
   directly — no rendering, no SPA fallback.
2. **Dispatches everything else to the server bundle.** Pages and `/api`
   endpoints are rendered on demand by the WebAssembly server bundle. This
   reuses the *exact* same machinery that runs regular Tau functions: the bundle
   is loaded from content-addressed storage (`/dfs/<cid>`), instantiated in the
   VM, and invoked with the incoming HTTP event.

Because the server bundle is just a WebAssembly function, SSR pages and `/api`
handlers inherit everything Tau functions already provide: cold-start pooling,
memory/timeout limits, SmartOps, metrics and the orbit plugin SDK.

Static websites are completely unaffected — an asset without a manifest is
served exactly as before.

## The SSR manifest

`__taubyte__/ssr.json` is produced by the framework adapter at build time and
consumed by the runtime. See [`pkg/specs/website/ssr.go`](../pkg/specs/website/ssr.go).

```jsonc
{
  "version": "1",
  "framework": "nextjs",
  "render": "ssr",                 // "ssr" or "static"
  "entry": "handle",               // wasm export invoked per request
  "handler": "__taubyte__/handler.wasm.zip",
  "memory": 268435456,             // VM memory limit, bytes (default 256 MiB)
  "timeout": 30000000000,          // per-request timeout, ns (default 30s)
  "static": [                      // prefixes always served from the asset
    "/_next/static/",
    "/_next/image"
  ],
  "routes": [                      // explicit classification, longest-prefix wins
    { "pattern": "/api/", "type": "api" },
    { "pattern": "/",     "type": "ssr" }
  ],
  "fallback": "ssr"                // for anything not matched above
}
```

`handler` may be replaced by `handlerCid` to point at an already-stored DAG
asset, avoiding a re-add at provision time.

Request classification (`Manifest.Classify`) is intent only: a request that
resolves to a real file in the asset is **always** served statically, even when
the manifest is coarse.

## Supported frameworks

Detection, default build commands and render mode live in
[`pkg/specs/builders/frameworks`](../pkg/specs/builders/frameworks). Frameworks
are recognised from `package.json` dependencies (and config files), with
meta-frameworks out-ranking the base libraries they build on (Next.js over
React, Nuxt over Vue, …).

| Framework            | Render | Static output     |
| -------------------- | ------ | ----------------- |
| Next.js              | SSR    | `.next`           |
| Nuxt                 | SSR    | `.output/public`  |
| SvelteKit            | SSR    | `build/client`    |
| Remix                | SSR    | `build/client`    |
| SolidStart           | SSR    | `.output/public`  |
| NestJS               | SSR    | —                 |
| Express/Fastify/Koa/Hono | SSR | —               |
| Astro                | Static | `dist`            |
| Gatsby               | Static | `public`          |
| Vite                 | Static | `dist`            |
| Angular              | Static | `dist`            |
| Create-React-App     | Static | `build`           |
| Vue (CLI)            | Static | `dist`            |
| Preact / Svelte / Solid | Static | `build` / `dist` |

## Zero-config deployment

Push a framework repository as a website. If it ships no `.taubyte`
configuration, the build pipeline detects the framework and generates one
automatically (see [`services/monkey/jobs/framework.go`](../services/monkey/jobs/framework.go)
and `frameworks.Generate`). A hand-written `.taubyte` directory always wins, so
you can drop down to full control whenever you need it.

The generated build:

- **static frameworks** — install, build, publish the static output directory.
- **SSR frameworks** — install, build, publish immutable assets, compile the
  server bundle to WebAssembly, and emit the manifest.

## The SSR adapter contract

Compiling a framework's server entry to WebAssembly is delegated to an **SSR
adapter** invoked by the generated build script:

```sh
taubyte-ssr-adapter --framework nextjs \
                    --entry .next/standalone/server.js \
                    --out  "$OUT/__taubyte__/handler.wasm.zip"
```

The adapter is provided by the build image and can be overridden with the
`TAUBYTE_SSR_ADAPTER` environment variable. It must produce
`handler.wasm.zip` — a zip containing `artifact.wasm` (the same format as a
regular Tau function asset) whose exported `entry` function:

1. reads the request from the HTTP event SDK,
2. invokes the framework's server handler,
3. writes status, headers and body back through the event SDK.

Bring your own toolchain (e.g. a QuickJS/Javy based bundler with a Tau HTTP
shim) by supplying a custom build image under `.taubyte/Docker`.

## Explicit configuration

SSR is driven by the asset manifest, so most deployments need no platform
configuration. To make a website claim **non-GET** `/api` methods
(POST/PUT/DELETE/…) before its first render, declare it on the website resource:

```yaml
render: ssr          # static (default) | ssr
framework: nextjs    # informational
entry: handle        # overrides the manifest entry
ssr-memory: 268435456
ssr-timeout: 30000000000
```

These map to fields on `structureSpec.Website` and are threaded through the
website schema (`render`, `framework`, `entry`). GET rendering works from the
manifest alone.

## Routing & precedence

- An explicitly defined **function** on a path always wins (it `HighMatch`es the
  exact path+method); an SSR website only claims a path with a lower,
  prefix-based score.
- Within an SSR website, real files beat the server bundle.
- The internal `__taubyte__/` directory is never part of the public surface.
