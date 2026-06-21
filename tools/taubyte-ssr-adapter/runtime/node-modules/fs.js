// node:fs stub. A WASM sandbox has no ambient filesystem — route data through
// Taubyte storage bindings instead. These exports exist so bundles that import
// fs resolve; calls that actually hit the disk fail loudly, and the cheap
// existence checks many libraries do at load time return "absent".

function unsupported(name) {
  return function () {
    throw new Error("node:fs." + name + " is not available in the Taubyte sandbox (no filesystem; use storage bindings)");
  };
}

export const existsSync = () => false;
export const readFileSync = unsupported("readFileSync");
export const writeFileSync = unsupported("writeFileSync");
export const readFile = (...a) => { const cb = a[a.length - 1]; if (typeof cb === "function") cb(new Error("node:fs.readFile unsupported")); };
export const writeFile = (...a) => { const cb = a[a.length - 1]; if (typeof cb === "function") cb(new Error("node:fs.writeFile unsupported")); };
export const statSync = unsupported("statSync");
export const stat = (...a) => { const cb = a[a.length - 1]; if (typeof cb === "function") cb(new Error("node:fs.stat unsupported")); };
export const lstatSync = unsupported("lstatSync");
export const readdirSync = () => [];
export const mkdirSync = () => undefined;
export const createReadStream = unsupported("createReadStream");
export const createWriteStream = unsupported("createWriteStream");
export const realpathSync = (p) => p;
export const accessSync = unsupported("accessSync");
export const constants = { F_OK: 0, R_OK: 4, W_OK: 2, X_OK: 1 };
export const promises = {
  readFile: () => Promise.reject(new Error("node:fs.promises.readFile unsupported")),
  writeFile: () => Promise.reject(new Error("node:fs.promises.writeFile unsupported")),
  stat: () => Promise.reject(new Error("node:fs.promises.stat unsupported")),
  mkdir: () => Promise.resolve(),
  readdir: () => Promise.resolve([]),
};

export default {
  existsSync, readFileSync, writeFileSync, readFile, writeFile, statSync, stat,
  lstatSync, readdirSync, mkdirSync, createReadStream, createWriteStream,
  realpathSync, accessSync, constants, promises,
};
