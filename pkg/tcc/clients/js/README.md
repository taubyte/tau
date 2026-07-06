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

`fs` is isomorphic-git's [lightning-fs](https://github.com/isomorphic-git/lightning-fs)
— or anything with the same async `promises` API (no hard dependency on it).

### Compile a repo

```ts
import LightningFS from "@isomorphic-git/lightning-fs";
import { compile } from "@taubyte/tcc";

const fs = new LightningFS("tau");
const { object, indexes, validations } = await compile(fs, "/my-project", {
  branch: "main",
});
```

### Edit a project with typed accessors

`open` / `decompile` return a `Session` — an **editable config representation that
lives inside the wasm module**. YAML is parsed and serialized only in wasm; the
generated getters/setters read/write typed fields across the wasm boundary, so
there's no YAML (or second YAML dialect) in TypeScript.

```ts
import { open, decompile } from "@taubyte/tcc";

// From a cloned repo's YAML...
const session = await open(fs, "/my-project");
// ...or by decompiling a compiled object:
// const session = await decompile(compiledObject);

const fn = session.function("api");          // typed accessor, addressed by name
await fn.setMemory("64GB");                   // source form is human-readable
await fn.setType("https");                    // "http" | "https" | "pubsub" | "p2p"
const t = await fn.type();                    // typed read

const { object } = await session.compile();   // compile the edited state
await session.save(fs, "/my-project");         // write the edits back as YAML
await session.close();
```

Accessors are generated from the tcc schema DSL by `tcc-gen --ts`: one class per
resource (`session.function`, `session.database`, …), each field mapped to its
config key (`memory` → `execution.memory`, `type` → `trigger.type`), `InSet` fields
typed as unions, and legacy keys read as a fallback. `makeSyncFs` / `hydrate` /
`flush` are also exported for lower-level filesystem control.

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
