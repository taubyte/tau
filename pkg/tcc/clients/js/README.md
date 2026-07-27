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

List, application scope, and delete mirror the Go schema:

```ts
await session.functionNames();               // names of top-level functions
await session.applications();                // application names
await session.functionNames("web");          // functions inside application "web"

const scoped = session.function("api", "web"); // application-scoped accessor
await scoped.setMemory("128MB");

await session.function("old").delete();       // remove a resource (pruned on save)
```

Accessors are generated from the tcc schema DSL by `tcc-gen --ts`: one class per
resource (`session.function`, `session.database`, …), each field mapped to its
config key (`memory` → `execution.memory`, `type` → `trigger.type`), `InSet` fields
typed as unions, and legacy keys read as a fallback. `makeSyncFs` / `hydrate` /
`flush` are also exported for lower-level filesystem control.

Applications and the project root are documents too, addressed the same way:

```ts
const app = session.application("web");       // the container's own config
await app.setDescription("the web app");
await session.project().setName("my_project"); // the project root document
```

### Whole-document editing

An editor holds a plain object, not a field at a time. Hand tcc the object and it
works out the minimal set/delete ops — so comments on untouched lines survive —
then hand back the one file that changed:

```ts
const fn = session.function("api");

const doc = await fn.doc();                   // the resource's whole document
await fn.setDoc({ ...doc, description: "edited" }); // minimal diff; missing keys delete

const { path, yaml } = await fn.serialize();  // "functions/api.yaml" + its exact YAML
await session.resourceAt(path);               // ["functions", "api"] — the inverse
```

Never build these paths yourself: where a resource's document lives is the DSL's
to decide (an application is `applications/web/config.yaml`, a function is
`functions/api.yaml`), and asserting a layout is how you end up reading a path
tcc never wrote.

Values the DSL declares as generated — a resource `id`, which is a CID — are
minted by tcc, not by the caller:

```ts
await fn.generate(["id"]);   // a fresh CID, seeded with the project and resource
```

### Validation

`validate()` on a resource is compile-free and per-field: one call returns every
local failure attributed to the field that caused it.

```ts
for (const { field, message } of await fn.validate()) {
  markField(field, message);                  // field: ["trigger", "domains"]
}
```

Whole-project checks still need `session.validate()`, which throws a `TccError`
carrying `file` / `line` / `column` — so "is this error in the file I'm editing?"
is a field comparison, not a substring match on the message.

```ts
try {
  await session.validate({ branch: "main" });
} catch (e) {
  if (e instanceof TccError && e.file !== myPath) { /* someone else's problem */ }
}
```

`session.resourceRepo(res)` returns the git repository backing a resource
(`{ provider, fullname, branch? }`) or `null`. The provider key is dynamic, so it
is read by shape — no provider is named.

`hydrate` skips dot-entries, so staging a git checkout never copies `.git` into
wasm, and a pruning `save` leaves them alone.

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
- `npm run test:browser` runs `src/browser.spec.ts` in a real headless Chrome via
  [`@web/test-runner`](https://modern-web.dev/docs/test-runner/overview/): it fetches
  and instantiates `assets/tcc.wasm` and drives compile/session exactly as a browser
  app would (uses the system Chrome; no download).
- `go test ./tools/tcc-gen/` runs a full pipeline test (`TestGeneratedClientE2E`):
  it regenerates the wasm and TS **fresh** into a temp package, drops this runtime
  and the tests alongside, and runs the Node tests + the browser tests against that
  generated code — validating it end to end, not just the committed `src/gen`. It
  skips under `-short`, or when node / deps / Chrome are absent.
