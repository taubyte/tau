// Loads and instantiates the tcc wasm module and exposes its compile/decompile
// entry points. The Go program registers `globalThis.tcc` synchronously during
// startup (before it blocks), so the exported functions are available as soon as
// go.run() returns control.

import type { SyncFs } from "./fs.js";

export interface CompileOptions {
  /** Git branch the compile is pinned to (default: the compiler's default). */
  branch?: string;
  /** Cloud FQDN to pin the compile to (optional). */
  cloud?: string;
}

export interface Validation {
  key: string;
  value: unknown;
  validator: string;
  context: Record<string, unknown>;
}

export interface CompileResult {
  object: Record<string, unknown>;
  indexes: Record<string, unknown>;
  validations: Validation[];
}

interface TccGlobal {
  compile(fs: SyncFs, opts?: CompileOptions): CompileResult | { error: string };
  decompile(obj: unknown, fs: SyncFs): null | { error: string };
}

/** The wasm binary and its Go loader script. Required outside Node. */
export interface WasmAssets {
  /** Source of the Go-provided wasm_exec.js (defines globalThis.Go). */
  wasmExecSource: string;
  /** The tcc.wasm bytes. */
  wasmBytes: BufferSource;
}

let cached: TccGlobal | null = null;

/**
 * loadWasm instantiates the tcc module once per process and returns its entry
 * points. In Node the bundled assets are read from disk; in the browser pass the
 * assets explicitly (e.g. fetched wasm_exec.js text + tcc.wasm bytes).
 */
export async function loadWasm(assets?: WasmAssets): Promise<TccGlobal> {
  if (cached) return cached;

  const a = assets ?? (await defaultAssets());

  const g = globalThis as unknown as { Go?: new () => GoRuntime; tcc?: TccGlobal };
  if (!g.Go) {
    // wasm_exec.js assigns globalThis.Go when executed in global scope.
    (0, eval)(a.wasmExecSource);
  }
  const go = new g.Go!();
  const { instance } = await WebAssembly.instantiate(a.wasmBytes, go.importObject);
  // Runs main() up to its blocking select{}, registering globalThis.tcc. We do
  // not await the returned promise — it only resolves when the program exits.
  void go.run(instance);

  if (!g.tcc) throw new Error("tcc wasm did not register globalThis.tcc");
  cached = g.tcc;
  return cached;
}

interface GoRuntime {
  importObject: WebAssembly.Imports;
  run(instance: WebAssembly.Instance): Promise<void>;
}

async function defaultAssets(): Promise<WasmAssets> {
  const proc = (globalThis as { process?: { versions?: { node?: string } } }).process;
  if (!proc?.versions?.node) {
    throw new Error(
      "loadWasm: pass { wasmExecSource, wasmBytes } outside Node (e.g. fetch them in the browser)",
    );
  }
  const { readFile } = await import("node:fs/promises");
  const { fileURLToPath } = await import("node:url");
  const { dirname, resolve } = await import("node:path");
  const here = dirname(fileURLToPath(import.meta.url));
  const assets = resolve(here, "..", "assets");
  return {
    wasmExecSource: await readFile(resolve(assets, "wasm_exec.js"), "utf8"),
    wasmBytes: await readFile(resolve(assets, "tcc.wasm")),
  };
}
