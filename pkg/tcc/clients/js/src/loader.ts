// Loads and instantiates the tcc wasm module and exposes its compile/decompile
// entry points. The Go program registers `globalThis.tcc` synchronously during
// startup (before it blocks), so the exported functions are available as soon as
// go.run() returns control.

import { makeSyncFs, flush, type SyncFs, type AsyncFs } from "./fs.js";

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

export interface TccGlobal {
  compile(fs: SyncFs, opts?: CompileOptions): CompileResult | { error: string };
  decompile(obj: unknown, fs: SyncFs): null | { error: string };
  /** The config JSON Schema (Draft 2020-12), generated from this wasm's own DSL. */
  schema(): Record<string, unknown> | { error: string };
  // Editable sessions (config lives in wasm; getters/setters address it by path).
  openSession(fs: SyncFs): number | { error: string };
  decompileSession(obj: unknown): number | { error: string };
  sessionGet(handle: number, resource: string[], field: string[]): unknown; // value | null(absent) | { error }
  sessionSet(handle: number, resource: string[], field: string[], value: unknown): null | { error: string };
  sessionCompile(handle: number, opts?: CompileOptions): CompileResult | { error: string };
  sessionValidate(handle: number, opts?: CompileOptions): { validations: Validation[] } | { error: string };
  sessionValidateField(handle: number, resource: string[], field: string[], value: unknown): null | { error: string };
  sessionValidateResource(handle: number, resource: string[]): { errors: string[] } | { error: string };
  sessionSave(handle: number, fs: SyncFs): null | { error: string };
  // field omitted -> delete the whole resource; field given -> unset that one field.
  sessionDelete(handle: number, resource: string[], field?: string[]): null | { error: string };
  sessionList(handle: number, path: string[]): string[] | { error: string };
  sessionFork(handle: number): number | { error: string };
  sessionMerge(handle: number): null | { error: string };
  sessionClose(handle: number): null;
}

/**
 * SessionBinding is the async facade the generated Session/accessor classes call.
 * Field access is async so the API is uniform with compile/save and future-proofs
 * a Worker/Atomics move, even though the wasm calls are synchronous today.
 */
export interface SessionBinding {
  get(handle: number, resource: string[], field: string[]): Promise<unknown>;
  set(handle: number, resource: string[], field: string[], value: unknown): Promise<void>;
  delete(handle: number, resource: string[], field?: string[]): Promise<void>;
  list(handle: number, path: string[]): Promise<string[]>;
  compile(handle: number, opts?: CompileOptions): Promise<CompileResult>;
  validate(handle: number, opts?: CompileOptions): Promise<Validation[]>;
  validateField(handle: number, resource: string[], field: string[], value: unknown): Promise<void>;
  validateResource(handle: number, resource: string[]): Promise<string[]>;
  save(handle: number, fs: AsyncFs, dir: string): Promise<void>;
  fork(handle: number): Promise<number>;
  merge(handle: number): Promise<void>;
  close(handle: number): Promise<void>;
}

function orThrow<T>(r: T | { error: string }): T {
  if (r && typeof r === "object" && "error" in r) throw new Error((r as { error: string }).error);
  return r as T;
}

/** makeBinding adapts the synchronous wasm session functions to SessionBinding. */
export function makeBinding(tcc: TccGlobal): SessionBinding {
  return {
    async get(handle, resource, field) {
      return orThrow(tcc.sessionGet(handle, resource, field) as unknown | { error: string });
    },
    async set(handle, resource, field, value) {
      orThrow(tcc.sessionSet(handle, resource, field, value));
    },
    async delete(handle, resource, field) {
      orThrow(tcc.sessionDelete(handle, resource, field));
    },
    async list(handle, path) {
      return orThrow(tcc.sessionList(handle, path));
    },
    async compile(handle, opts) {
      return orThrow(tcc.sessionCompile(handle, opts));
    },
    async validate(handle, opts) {
      return orThrow(tcc.sessionValidate(handle, opts)).validations;
    },
    async validateField(handle, resource, field, value) {
      orThrow(tcc.sessionValidateField(handle, resource, field, value));
    },
    async validateResource(handle, resource) {
      return orThrow(tcc.sessionValidateResource(handle, resource)).errors;
    },
    async save(handle, fs, dir) {
      const sync = makeSyncFs();
      orThrow(tcc.sessionSave(handle, sync));
      await flush(fs, dir, sync.map, { prune: true }); // reflect deletions
    },
    async fork(handle) {
      return orThrow(tcc.sessionFork(handle));
    },
    async merge(handle) {
      orThrow(tcc.sessionMerge(handle));
    },
    async close(handle) {
      tcc.sessionClose(handle);
    },
  };
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
