// Filesystem bridge between the browser and the tcc wasm module.
//
// The wasm module (see pkg/tcc/wasm) drives the compiler/decompiler, which read
// and write YAML through a *synchronous* filesystem. Browser filesystems such as
// isomorphic-git's lightning-fs are asynchronous, so we stage the (small) project
// tree into an in-memory Map, expose synchronous primitives over it to the wasm,
// and flush the Map back afterwards.

/** Synchronous filesystem primitives handed to the wasm module. */
export interface SyncFs {
  readFile(path: string): Uint8Array | null;
  writeFile(path: string, data: Uint8Array): void;
  readdir(path: string): string[];
  stat(path: string): { isDir: boolean; size: number } | null;
  mkdir(path: string): void;
}

/**
 * makeSyncFs builds SyncFs primitives over an in-memory Map of absolute path ->
 * bytes. Directories are tracked explicitly (as lightning-fs and afero.MemMapFs
 * do) because the compiler's seer calls mkdir and then expects stat to report
 * the directory within the same operation.
 */
export function makeSyncFs(
  map: Map<string, Uint8Array> = new Map(),
): SyncFs & { map: Map<string, Uint8Array> } {
  const dirs = new Set<string>(["/"]);
  const registerParents = (p: string) => {
    let d = p;
    for (;;) {
      const i = d.lastIndexOf("/");
      if (i <= 0) {
        dirs.add("/");
        break;
      }
      d = d.slice(0, i);
      dirs.add(d);
    }
  };
  for (const k of map.keys()) registerParents(k);

  const isDir = (p: string) => {
    if (dirs.has(p)) return true;
    const pre = p.endsWith("/") ? p : p + "/";
    for (const k of map.keys()) if (k.startsWith(pre)) return true;
    return false;
  };

  return {
    map,
    readFile: (p) => (map.has(p) ? map.get(p)! : null),
    writeFile: (p, data) => {
      map.set(p, data);
      registerParents(p);
    },
    readdir: (p) => {
      const pre = p === "/" ? "/" : p + "/";
      const names = new Set<string>();
      const add = (k: string) => {
        if (k.startsWith(pre)) {
          const n = k.slice(pre.length).split("/")[0];
          if (n) names.add(n);
        }
      };
      for (const k of map.keys()) add(k);
      for (const d of dirs) if (d !== "/") add(d);
      return [...names];
    },
    stat: (p) => {
      if (map.has(p)) return { isDir: false, size: map.get(p)!.length };
      if (isDir(p)) return { isDir: true, size: 0 };
      return null;
    },
    mkdir: (p) => {
      dirs.add(p);
    },
  };
}

/**
 * AsyncFs is the minimal slice of the isomorphic-git lightning-fs `promises` API
 * we depend on. Declared structurally so this package needs no runtime dependency
 * on lightning-fs.
 */
export interface AsyncFs {
  promises: {
    readFile(path: string): Promise<Uint8Array | string>;
    writeFile(path: string, data: Uint8Array): Promise<void>;
    readdir(path: string): Promise<string[]>;
    stat(path: string): Promise<{ isDirectory(): boolean }>;
    mkdir(path: string): Promise<void>;
    /** Optional — used to prune deleted files when saving a session. */
    unlink?(path: string): Promise<void>;
  };
}

const joinPath = (a: string, b: string) =>
  (a.endsWith("/") ? a + b : a + "/" + b).replace(/\/{2,}/g, "/");

/** Read every file under `dir` in an async fs into a compiler-rooted ("/") Map. */
export async function hydrate(fs: AsyncFs, dir: string): Promise<Map<string, Uint8Array>> {
  const map = new Map<string, Uint8Array>();
  const walk = async (abs: string, rooted: string) => {
    for (const name of await fs.promises.readdir(abs)) {
      const childAbs = joinPath(abs, name);
      const childRooted = joinPath(rooted, name);
      const st = await fs.promises.stat(childAbs);
      if (st.isDirectory()) {
        await walk(childAbs, childRooted);
      } else {
        const data = await fs.promises.readFile(childAbs);
        map.set(childRooted, typeof data === "string" ? new TextEncoder().encode(data) : data);
      }
    }
  };
  await walk(dir, "/");
  return map;
}

/**
 * Write a compiler-rooted Map back under `dir` in an async fs, creating dirs.
 * With `prune`, files under `dir` not in the map are removed (so a saved session
 * reflects deletions), if the fs supports `unlink`.
 */
export async function flush(
  fs: AsyncFs,
  dir: string,
  map: Map<string, Uint8Array>,
  opts: { prune?: boolean } = {},
): Promise<void> {
  const mkdirp = async (rooted: string) => {
    let cur = "";
    for (const part of rooted.split("/").filter(Boolean)) {
      cur += "/" + part;
      try {
        await fs.promises.mkdir(joinPath(dir, cur));
      } catch {
        // already exists
      }
    }
  };
  for (const [rooted, data] of map) {
    const slash = rooted.lastIndexOf("/");
    if (slash > 0) await mkdirp(rooted.slice(0, slash));
    await fs.promises.writeFile(joinPath(dir, rooted), data);
  }

  const unlink = fs.promises.unlink?.bind(fs.promises);
  if (opts.prune && unlink) {
    const prune = async (abs: string, rooted: string) => {
      let names: string[];
      try {
        names = await fs.promises.readdir(abs);
      } catch {
        return;
      }
      for (const name of names) {
        const childAbs = joinPath(abs, name);
        const childRooted = joinPath(rooted, name);
        if ((await fs.promises.stat(childAbs)).isDirectory()) {
          await prune(childAbs, childRooted);
        } else if (!map.has(childRooted)) {
          await unlink(childAbs);
        }
      }
    };
    await prune(dir, "/");
  }
}
