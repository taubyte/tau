# Web hosting architecture & extension guide

A map of the SSR / framework hosting subsystem and the three places you extend
it. For capabilities see [web-frameworks.md](web-frameworks.md); for the runtime
plan see [js-runtime-roadmap.md](js-runtime-roadmap.md).

## Components

| Layer | Package | Role |
| --- | --- | --- |
| **Manifest spec** | `pkg/specs/website` (`ssr.go`) | The self-describing SSR manifest (`__taubyte__/ssr.json`): render mode, handler **ABI**, routes, static prefixes, memory/timeout. Parse/validate/classify. |
| **Website schema** | `pkg/specs/structure`, `pkg/schema/website` | `render`/`framework`/`entry` fields on the website resource. |
| **Framework registry** | `pkg/specs/builders/frameworks` | Detect a framework from `package.json`; support tier (`AdapterKind`); generate a zero-config `.taubyte` build. |
| **Next translator** | `pkg/specs/builders/frameworks/nextjs` | `.next/` manifests → Taubyte SSR manifest + routing Report; assemble the asset. |
| **JS adapter** | `tools/taubyte-ssr-adapter` | Bundle a JS handler (esbuild) + runtime shims, compile to wasm (Javy), package. Modes: `handler` (JSON) / `fetch` (Web-standard) [+`--node`]. |
| **Next adapter** | `tools/taubyte-next-adapter` | Next build → website asset (static + prerendered + manifest + handler). |
| **Runtime shims** | `tools/taubyte-ssr-adapter/runtime` | `web.js` (Web APIs), `node.js` (Node globals), `node-modules/*` (node builtin modules). |
| **Serving** | `services/substrate/components/http/website` | Pick static vs SSR; dispatch by ABI via the engine registry; run the bundle. |
| **VM** | `pkg/vm`, `core/vm` | wazero (core wasm + WASI-P1) with stdin support. |
| **Build pipeline** | `services/monkey/jobs` | Detect framework + materialize the build when a repo ships no `.taubyte`. |

## Request flow (SSR)

```
request ─▶ http lookup ─▶ website.Handle
                           ├─ static file in asset?  ─▶ serve from zip
                           └─ else serveSSR ─▶ ssrEngines[abi]
                                                ├─ function   → pooled wasm + go-sdk HTTP event
                                                ├─ wasi-stdio → per-request wasm, request on stdin / response on stdout
                                                └─ component  → ComponentRuntime backend (when registered)
```

## Extension points

### 1. Add a framework
Add an entry to `Registry` in `frameworks.go` (deps to detect, render mode,
build commands, static dir, server entry) and classify it in `adapterByName`
(`static`/`fetch`/`next`/`node`). `Generate` then emits the right build. Add a
detection test.

### 2. Add a handler ABI / engine
Register a `renderFunc` in `ssrEngines` (`engine.go`). For a richer JS engine,
implement `ComponentRuntime` (`component.go`) in its own package and call
`website.RegisterComponentRuntime(...)` from `init()` — this enables
`ABIComponent` without pulling the engine's dependency into the substrate core.
Unsupported ABIs fail fast.

### 3. Add a runtime API (polyfill / node module)
- A **global** (`fetch`, `crypto`, …): add to `runtime/web.js` or `runtime/node.js`
  behind a `typeof g.X === "undefined"` guard.
- A **node builtin** (`node:fs`, …): add `runtime/node-modules/<name>.js` and an
  alias in `main.go` (`--alias:node:<name>=...` and `--alias:<name>=...`).
Verify in node (`new Function(fs.readFileSync(...))()`), then end to end via
`javy` + `wasmtime`.

## Build / test matrix

| What | Command | Needs |
| --- | --- | --- |
| Logic | `go test ./pkg/specs/website/ ./pkg/specs/builders/frameworks/... ./services/substrate/components/http/website/ ./tools/taubyte-ssr-adapter/` | nothing |
| Live SSR (function/stdio/Hono) | `go test -tags dreaming -run 'TestWebsiteSSR_Dreaming|TestWebsiteSSRStdio_Dreaming|TestWebsiteHono_Dreaming' ./services/monkey/fixtures/compile/` | TinyGo (+ npm/javy/esbuild for Hono) |
| JS render (no substrate) | `echo '{"method":"GET","url":"/"}' \| wasmtime main.wasm` | javy + wasmtime |
| Polyfill unit | `node -e 'new Function(require("fs").readFileSync("runtime/web.js","utf8"))()'` | node |

Docker is **not** needed (the `dreaming` tests push assets via `compileFor`'s
zip path). The only Docker-dependent tests in the repo are the pre-existing
`pkg/builder` integration tests.
