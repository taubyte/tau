// @taubyte/tcc — compile a Taubyte config repo in the browser, and edit it via a
// typed session whose config representation lives in WebAssembly (Go). This module
// wires the wasm to a browser filesystem (isomorphic-git's lightning-fs, or any
// object with the same async `promises` API) and exposes the typed API.

import { hydrate, makeSyncFs, type AsyncFs } from "./fs.js";
import {
  loadWasm,
  makeBinding,
  type CompileOptions,
  type CompileResult,
  type WasmAssets,
} from "./loader.js";
import { Session } from "./gen/schema.js";

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
  const res = tcc.compile(makeSyncFs(await hydrate(fs, dir)), opts);
  if ("error" in res) throw new Error(res.error);
  return res;
}

/**
 * return the config JSON Schema (Draft 2020-12) describing every resource, its
 * fields, constraints, and cross-references — generated from the wasm's own DSL,
 * so it always matches this build. Useful for UI generation and agent tooling.
 */
export async function schema(assets?: WasmAssets): Promise<Record<string, unknown>> {
  const tcc = await loadWasm(assets);
  const res = tcc.schema();
  if ("error" in res) throw new Error(res.error as string);
  return res;
}

/**
 * open an editable {@link Session} over a project's YAML under `dir` (parsed into
 * a wasm-resident representation). Edit typed fields via `session.function(name)`
 * etc., then `session.compile()` or `session.save(fs, dir)`.
 */
export async function open(fs: AsyncFs, dir: string, assets?: WasmAssets): Promise<Session> {
  const tcc = await loadWasm(assets);
  const handle = tcc.openSession(makeSyncFs(await hydrate(fs, dir)));
  if (typeof handle !== "number") throw new Error(handle.error);
  return new Session(makeBinding(tcc), handle);
}

/**
 * decompile a compiled object into an editable {@link Session} (the config becomes
 * a wasm-resident, editable representation). `obj` is the value from {@link compile}.
 */
export async function decompile(obj: unknown, assets?: WasmAssets): Promise<Session> {
  const tcc = await loadWasm(assets);
  const handle = tcc.decompileSession(obj);
  if (typeof handle !== "number") throw new Error(handle.error);
  return new Session(makeBinding(tcc), handle);
}
