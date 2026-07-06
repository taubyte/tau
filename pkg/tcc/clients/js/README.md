# @taubyte/tcc

Compile and decompile a Taubyte config repo **in the browser**. The
compile/decompile core is the same Go engine used server-side, compiled to
WebAssembly; this package wires it to a browser filesystem and gives you a typed
API.

## Building the wasm assets

The `.wasm` binary and its `wasm_exec.js` loader are produced by the `tcc-gen`
tool (they are not committed — see `.gitignore`). From the repo root:

```sh
go run ./tools/tcc-gen --wasm            # -> pkg/tcc/clients/js/assets/
# or redirect elsewhere (tests, //go:embed, ...):
go run ./tools/tcc-gen --wasm --out /some/dir
```

Then build the TS: `npm install && npm run build`.

### Smaller binary (TinyGo, optional)

For roughly half the size (~3.9MB raw / ~1.3MB gzip vs ~8.2MB / ~2.2MB), build with
TinyGo in a container instead. It patches `spf13/afero` (which pulls `net/http` and
uses `os.Chmod`/`Chown` unavailable under TinyGo's wasm target — all dead code in the
browser) via a throwaway `go mod replace`; the repo is not modified. Requires Docker:

```sh
pkg/tcc/wasm/tinygo-build.sh                 # -> pkg/tcc/clients/js/assets/
pkg/tcc/wasm/tinygo-build.sh /some/dir       # or elsewhere
```

The output drops into the same `assets/` (its own `wasm_exec.js` included), so the
package works unchanged with either build. The standard `go` build is the default and
is the more conservative choice.

## Usage

With isomorphic-git's [lightning-fs](https://github.com/isomorphic-git/lightning-fs)
(or any object with the same async `promises` API):

```ts
import LightningFS from "@isomorphic-git/lightning-fs";
import { compile, decompile } from "@taubyte/tcc";

const fs = new LightningFS("tau");

// Compile the project rooted at /my-project into the compiled object + indexes.
const { object, indexes, validations } = await compile(fs, "/my-project", {
  branch: "main",
});

// Render a compiled object back to YAML files under /out.
await decompile(fs, "/out", { object, indexes });
```

`fs` is anything with lightning-fs's async `promises` API — no hard dependency on
lightning-fs itself. For lower-level control (e.g. an in-memory `Map` of files),
`makeSyncFs` / `hydrate` / `flush` are exported building blocks.

### Typed resource accessors

`src/gen/schema.ts` is generated from the tcc schema DSL by `tcc-gen --ts` — one
accessor class per resource whose typed getters/setters map each flat field to its
nested config key (`memory` → `execution.memory`, `type` → `trigger.type`), with
`InSet` fields typed as unions and legacy keys read as a fallback. Pure TypeScript
over a plain config object (no YAML — tcc handles that):

```ts
import { FunctionConfig } from "@taubyte/tcc";

const fn = new FunctionConfig();
fn.type = "https";          // -> data.trigger.type ("http" | "https" | "pubsub" | "p2p")
fn.memory = 64_000_000;     // -> data.execution.memory
// fn.data is the nested config object.
```

### Outside Node

In the browser, fetch the assets and pass them explicitly (Node auto-loads them
from disk):

```ts
const assets = {
  wasmExecSource: await (await fetch("/assets/wasm_exec.js")).text(),
  wasmBytes: await (await fetch("/assets/tcc.wasm")).arrayBuffer(),
};
await compile(fs, "/my-project", { branch: "main" }, assets);
```

## Notes

- The filesystem bridge stages the (small) project tree into an in-memory map and
  exposes **synchronous** primitives to the wasm, since the compiler's fs access
  is synchronous while lightning-fs is async.
- `npm test` runs the golden compile/decompile round-trip against the repo's tcc
  fixtures (requires the assets to have been built first).
