// Loads the v2 DSL compiled to WebAssembly. This is tcc itself running in the
// browser — the same compiler the Go tests use, over the same schema declaration.
// Nothing here reimplements the DSL; if a field compiles here it compiles on a node.

export type Effect = "read" | "write" | "admin";
export interface Verb {
  name: string;
  effect: Effect;
}
export interface KindVerbs {
  kind: string;
  verbs: Verb[];
}
export interface Kind {
  group: string;
  singular: string;
  container: boolean;
  category: string;
  doc: string;
  icon: string;
  fields: string[];
}
export interface Omission {
  name: string;
  state: "never" | "blocked" | "open";
  why: string;
  instead: string;
  unblocks: string;
}
export type Compiled = Record<string, unknown>;
export interface CompileOk {
  object: Compiled;
  validations?: { key: string; value: unknown; validator: string }[];
}
export type CompileResult = CompileOk | { error: string };

interface TccV2 {
  compile(files: Record<string, string>, opts?: { branch?: string }): CompileResult;
  schema(): Record<string, unknown>;
  verbs(): KindVerbs[];
  kinds(): Kind[];
  omissions(): Omission[];
}

declare global {
  interface Window {
    tccV2?: TccV2;
    Go: new () => { importObject: WebAssembly.Imports; run(i: WebAssembly.Instance): void };
  }
}

let ready: Promise<TccV2> | null = null;

export function loadTcc(): Promise<TccV2> {
  if (ready) return ready;
  ready = (async () => {
    await loadScript(`${import.meta.env.BASE_URL}wasm_exec.js`);
    const go = new window.Go();
    // no-cache, not because the wasm is large but because it is REGENERATED: every
    // `tcc-gen --dsl v2 --wasm` writes a new schema to the same URL, and public/ is
    // outside the module graph so Vite has no hash to bust and no HMR edge to fire.
    // A cached copy is a page showing a vocabulary that no longer exists — a kind
    // added to the DSL simply does not appear, and nothing says why. Revalidate.
    const src = await WebAssembly.instantiateStreaming(
      fetch(`${import.meta.env.BASE_URL}tcc.wasm`, { cache: "no-cache" }),
      go.importObject,
    );
    // go.run never resolves: the Go side parks on `select {}` so the exported
    // functions stay callable for the life of the page.
    void go.run(src.instance);
    for (let i = 0; i < 200 && !window.tccV2; i++) await sleep(10);
    if (!window.tccV2) throw new Error("tcc wasm did not register globalThis.tccV2");
    return window.tccV2;
  })();
  return ready;
}

function loadScript(src: string): Promise<void> {
  return new Promise((resolve, reject) => {
    const s = document.createElement("script");
    s.src = src;
    s.onload = () => resolve();
    s.onerror = () => reject(new Error(`failed to load ${src}`));
    document.head.appendChild(s);
  });
}

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));
