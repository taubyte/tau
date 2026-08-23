// Loads the v2 DSL compiled to WebAssembly. This is tcc itself running in the
// browser — the same compiler the Go tests use, over the same schema declaration.
// Nothing here reimplements the DSL; if a field compiles here it compiles on a node.

export type Effect = "read" | "write" | "admin";
export interface Verb {
  name: string;
  effect: Effect;
  // the platform hands the work over, rather than the resource going to get it — a
  // container consuming one of these needs a port for the items to land on
  delivered: boolean;
  // which way the edge points: true when the action travels TOWARD the resource that
  // declared it, which is the difference between an arrow that points at a queue and
  // one that comes out of it. Every delivered verb is inbound; a secret's `get` is
  // inbound without being pushed — the value moves out of the secret either way.
  inbound: boolean;
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
/** What a consumer would otherwise have to know about this vocabulary. From tcc. */
/**
 * What a consumer would otherwise have to know, read off the schema.
 *
 * It holds what is ASKED FOR and nothing else. Four more facts used to be here — every
 * reference a kind declares, the blocks that hand traffic on, what an access entry is
 * called, where each kind states the name it answers on — and the page stopped reading
 * all four once tcc began answering the questions they were for (resources, relations,
 * names). A shape nobody reads is a second definition waiting to disagree with the first.
 */
export interface Shapes {
  /** Where a document lives, as templates. */
  layout: { resource: string; scope: string; leaf: string };
  /** Per kind: the fields it DERIVES, which only the engine can compute. */
  kinds: Record<string, { derived?: { field: string; derivation: string; from: string[]; label: string }[] }>;
}

export interface Omission {
  name: string;
  state: "never" | "blocked" | "open";
  why: string;
  instead: string;
  unblocks: string;
}
export type Compiled = Record<string, unknown>;

/** One cross-resource finding, decided by tcc — see schema/checks.go. */
export interface Finding {
  severity: "broken" | "check" | "note";
  kind: string;
  name: string;
  scope: string;
  title: string;
  detail: string;
  /** The compiled resource this is about, empty when there genuinely is none — an
   *  unresolved reference, or two candidates for one slot with no single winner. */
  id: string;
}

/** One compiled resource. What a resource is, and where it lives, is decided by tcc. */
export interface Instance {
  id: string;
  kind: string;
  name: string;
  /** The namespace path it lives in, "" at the project root. */
  scope: string;
  /** The compiled resource itself, for the values a consumer reads off it. */
  fields: Compiled;
}

/** One resolved relationship between two resources. Resolved by tcc — see relations.go. */
export interface Relation {
  from: string;
  to: string;
  kind: string;
  verb: string;
  /** read | write | admin for a use; empty for a reference, which grants nothing. */
  effect?: string;
  class: "use" | "handoff" | "policy" | "reference";
}

/** What a compile produced that nobody wrote. From tcc — see interp/lowered.go. */
export interface LoweredProject {
  /** Every produced resource, by its compiled id, and the authored resource it came from. */
  produced: Record<
    string,
    {
      from: string;
      at: string;
      kind: string;
      name: string;
      scope: string;
    }
  >;
  /** Every value a lowering wrote into a resource somebody DID write: a path and a value. */
  granted: { on: string; path: string[]; value: string; from: string }[];
  /** The project AS IT RUNS: the same shapes as the authored one, because it is one. */
  resources: Instance[];
  relations: Relation[];
}

export interface CompileOk {
  findings?: Finding[];
  /** What each resource is reached at. Computed by tcc: a name is three places glued. */
  names?: Record<string, { name: string; zone: string; fqdn: string; generated: boolean }>;
  /** Every resource in the project, found by tcc: a walk that needs the schema. */
  resources?: Instance[];
  /**
   * Every SCOPE, which is a different question from every resource: `resources` lists
   * what is INSIDE a scope, on purpose. A scope path is a path of names and a document
   * address is a path of directories named after the KIND — `shop/checkout` is
   * `namespace/shop/workflow/checkout` — and there is more than one kind of scope, so this
   * is where the kind comes from rather than from a page assuming one.
   */
  scopes?: Instance[];
  /** Every relationship between two resources, already resolved and already directed. */
  relations?: Relation[];
  /** The project as it RUNS, and what in it nobody wrote. */
  lowered?: LoweredProject;
  /**
   * Every document the second pass actually wrote, by path: a relocated source's own
   * leaf document, and each produced child, exactly as read back — not the compiled
   * object's shape of them, the yaml. Empty for a project that lowers nothing.
   */
  loweredFiles?: Record<string, string>;
  /** The project's label in a cloud's generated zone — derived from its id. */
  projectLabel?: string;
  object: Compiled;
  validations?: { key: string; value: unknown; validator: string }[];
}
export type CompileResult = CompileOk | { error: string };

interface TccV2 {
  compile(files: Record<string, string>, opts?: { branch?: string }): CompileResult;
  schema(): Record<string, unknown>;
  verbs(): KindVerbs[];
  kinds(): Kind[];
  shapes(): Shapes;
  /**
   * What the resources nobody wrote WOULD be, as config: decompile, all the way, minus
   * the files the caller already has. Called when a box is opened, not on every
   * keystroke — it is a whole decompile, and nothing needs it until somebody looks.
   */
  generated(files: Record<string, string>, opts?: { branch?: string }): Record<string, string> | { error: string };
  /** Compute a derived value. The derivation is the engine's, so nobody reimplements it. */
  derive(derivation: string, ...inputs: string[]): string;
  omissions(): Omission[];
}

declare global {
  interface Window {
    tccV2?: TccV2;
    Go: new () => { importObject: WebAssembly.Imports; run(i: WebAssembly.Instance): void };
  }
}

// One token per page load: enough to defeat a cached pair, and identical for the two
// files so they can never be fetched from different generations.
const BUILD = Date.now().toString(36);

let ready: Promise<TccV2> | null = null;

export function loadTcc(): Promise<TccV2> {
  if (ready) return ready;
  ready = (async () => {
    // Both files are REGENERATED by `tcc-gen --wasm` at a stable URL, and public/ is
    // outside the module graph so Vite has no hash to bust and no HMR edge to fire.
    // A cached copy is a page compiling against a vocabulary that no longer exists.
    //
    // They are a PAIR and must match: wasm_exec.js is the Go runtime glue for the
    // exact toolchain that built tcc.wasm. Busting only the wasm — which is what this
    // did at first — pairs a fresh module with stale glue, and the page dies inside Go
    // with "Cannot read properties of undefined", naming nothing that suggests a cache.
    const stamp = `?v=${BUILD}`;
    await loadScript(`${import.meta.env.BASE_URL}wasm_exec.js${stamp}`);
    const go = new window.Go();
    const src = await WebAssembly.instantiateStreaming(
      fetch(`${import.meta.env.BASE_URL}tcc.wasm${stamp}`, { cache: "no-cache" }),
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
