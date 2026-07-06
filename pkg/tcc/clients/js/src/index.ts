// @taubyte/tcc — compile and decompile a Taubyte config repo in the browser.
//
// The compile/decompile core runs in WebAssembly (Go); this module wires it to a
// browser filesystem (isomorphic-git's lightning-fs, or any Map of files) and
// exposes a typed API.

import { hydrate, flush, makeSyncFs, type AsyncFs } from "./fs.js";
import {
  loadWasm,
  type CompileOptions,
  type CompileResult,
  type WasmAssets,
} from "./loader.js";

export * from "./fs.js";
export * from "./loader.js";
export * from "./gen/schema.js";

/**
 * compile a project stored under `dir` in an async filesystem (e.g. lightning-fs)
 * and return the compiled object, indexes, and external validations.
 */
export async function compile(
  fs: AsyncFs,
  dir: string,
  opts: CompileOptions = {},
  assets?: WasmAssets,
): Promise<CompileResult> {
  const tcc = await loadWasm(assets);
  const sync = makeSyncFs(await hydrate(fs, dir));
  const res = tcc.compile(sync, opts);
  if ("error" in res) throw new Error(res.error);
  return res;
}

/**
 * decompile a previously compiled object back into YAML files written under
 * `dir` in an async filesystem. `obj` is the value returned by {@link compile}.
 */
export async function decompile(
  fs: AsyncFs,
  dir: string,
  obj: unknown,
  assets?: WasmAssets,
): Promise<void> {
  const tcc = await loadWasm(assets);
  const sync = makeSyncFs();
  const err = tcc.decompile(obj, sync);
  if (err && err.error) throw new Error(err.error);
  await flush(fs, dir, sync.map);
}

/**
 * compileMap / decompileMap operate directly on an in-memory Map<path, bytes>
 * (compiler-rooted at "/"), for callers that manage their own filesystem or for
 * tests. compileMap reads the map; decompileMap returns a fresh map of YAML.
 */
export async function compileMap(
  files: Map<string, Uint8Array>,
  opts: CompileOptions = {},
  assets?: WasmAssets,
): Promise<CompileResult> {
  const tcc = await loadWasm(assets);
  const res = tcc.compile(makeSyncFs(files), opts);
  if ("error" in res) throw new Error(res.error);
  return res;
}

export async function decompileMap(
  obj: unknown,
  assets?: WasmAssets,
): Promise<Map<string, Uint8Array>> {
  const tcc = await loadWasm(assets);
  const sync = makeSyncFs();
  const err = tcc.decompile(obj, sync);
  if (err && err.error) throw new Error(err.error);
  return sync.map;
}
