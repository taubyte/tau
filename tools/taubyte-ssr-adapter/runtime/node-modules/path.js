// node:path (posix) shim — pure-JS, no filesystem. Enough of the path API that
// Express/`send`/`serve-static` and friends use for URL/path manipulation.

function assertPath(p) {
  if (typeof p !== "string") throw new TypeError("Path must be a string. Received " + typeof p);
}

export function normalize(path) {
  assertPath(path);
  if (path.length === 0) return ".";
  const isAbs = path.charCodeAt(0) === 47; // "/"
  const trailing = path.charCodeAt(path.length - 1) === 47;
  const parts = path.split("/");
  const out = [];
  for (const p of parts) {
    if (p === "" || p === ".") continue;
    if (p === "..") {
      if (out.length && out[out.length - 1] !== "..") out.pop();
      else if (!isAbs) out.push("..");
    } else out.push(p);
  }
  let res = out.join("/");
  if (!res) res = isAbs ? "/" : ".";
  if (isAbs) res = "/" + res;
  else if (trailing && res !== ".") res += "/";
  else if (trailing) res = "./";
  return res;
}

export function join(...parts) {
  if (parts.length === 0) return ".";
  const joined = parts.filter((p) => {
    assertPath(p);
    return p.length > 0;
  }).join("/");
  return joined.length === 0 ? "." : normalize(joined);
}

export function isAbsolute(path) {
  assertPath(path);
  return path.length > 0 && path.charCodeAt(0) === 47;
}

export function resolve(...parts) {
  let resolved = "";
  let isAbs = false;
  for (let i = parts.length - 1; i >= 0 && !isAbs; i--) {
    const p = parts[i];
    assertPath(p);
    if (p.length === 0) continue;
    resolved = p + "/" + resolved;
    isAbs = p.charCodeAt(0) === 47;
  }
  if (!isAbs) resolved = "/" + resolved; // no real cwd in the sandbox; root-relative
  const norm = normalize(resolved);
  return norm.length > 1 && norm.endsWith("/") ? norm.slice(0, -1) : norm;
}

export function dirname(path) {
  assertPath(path);
  if (path.length === 0) return ".";
  let end = -1;
  let matched = false;
  for (let i = path.length - 1; i >= 1; i--) {
    if (path.charCodeAt(i) === 47) {
      if (matched) { end = i; break; }
    } else matched = true;
  }
  if (end === -1) return path.charCodeAt(0) === 47 ? "/" : ".";
  if (end === 0) return "/";
  return path.slice(0, end);
}

export function basename(path, ext) {
  assertPath(path);
  let start = 0, end = -1, matched = false;
  for (let i = path.length - 1; i >= 0; i--) {
    if (path.charCodeAt(i) === 47) {
      if (matched) { start = i + 1; break; }
    } else { matched = true; if (end === -1) end = i + 1; }
  }
  if (end === -1) return "";
  let base = path.slice(start, end);
  if (ext && base.endsWith(ext) && base !== ext) base = base.slice(0, base.length - ext.length);
  return base;
}

export function extname(path) {
  assertPath(path);
  let dot = -1, start = -1;
  for (let i = path.length - 1; i >= 0; i--) {
    const c = path.charCodeAt(i);
    if (c === 47) { if (start !== -1) break; continue; }
    if (start === -1) start = i;
    if (c === 46 && dot === -1) dot = i;
  }
  if (dot === -1 || dot === start || path.charCodeAt(dot - 1) === 47) return "";
  return path.slice(dot);
}

export function parse(path) {
  assertPath(path);
  const root = path.charCodeAt(0) === 47 ? "/" : "";
  const dir = dirname(path);
  const base = basename(path);
  const ext = extname(path);
  const name = ext ? base.slice(0, base.length - ext.length) : base;
  return { root, dir: dir === "." && !root ? "" : dir, base, ext, name };
}

export function format(obj) {
  const dir = obj.dir || obj.root || "";
  const base = obj.base || (obj.name || "") + (obj.ext || "");
  if (!dir) return base;
  if (dir === obj.root) return dir + base;
  return dir + "/" + base;
}

export function relative(from, to) {
  from = resolve(from);
  to = resolve(to);
  if (from === to) return "";
  const fp = from.split("/").filter(Boolean);
  const tp = to.split("/").filter(Boolean);
  let i = 0;
  while (i < fp.length && i < tp.length && fp[i] === tp[i]) i++;
  const up = fp.slice(i).map(() => "..");
  return up.concat(tp.slice(i)).join("/");
}

export const sep = "/";
export const delimiter = ":";
export const posix = { normalize, join, isAbsolute, resolve, dirname, basename, extname, parse, format, relative, sep, delimiter };
export const win32 = posix; // no real win32 in the sandbox
posix.posix = posix;
posix.win32 = posix;

export default posix;
