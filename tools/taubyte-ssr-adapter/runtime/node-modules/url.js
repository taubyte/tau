// node:url shim. WHATWG URL/URLSearchParams come from the platform
// (StarlingMonkey-native); this adds the legacy url.parse/format/resolve API
// (still used by Express internals and older deps) on top.

export const URL = globalThis.URL;
export const URLSearchParams = globalThis.URLSearchParams;

// Legacy url.parse — returns a Url-like object. parseQueryString=true turns
// `query` into an object (via querystring), otherwise it stays a string.
export function parse(urlStr, parseQueryString = false, slashesDenoteHost = false) {
  const out = {
    protocol: null, slashes: null, auth: null, host: null, port: null,
    hostname: null, hash: null, search: null, query: null, pathname: null,
    path: null, href: urlStr,
  };
  if (typeof urlStr !== "string") return out;

  // Relative path-only URL (the common case for server-side req.url).
  const hasScheme = /^[a-zA-Z][a-zA-Z0-9+.-]*:/.test(urlStr);
  if (!hasScheme) {
    let rest = urlStr;
    const hashIdx = rest.indexOf("#");
    if (hashIdx !== -1) { out.hash = rest.slice(hashIdx); rest = rest.slice(0, hashIdx); }
    const qIdx = rest.indexOf("?");
    if (qIdx !== -1) {
      out.search = rest.slice(qIdx);
      out.query = parseQueryString ? qsParse(rest.slice(qIdx + 1)) : rest.slice(qIdx + 1);
      rest = rest.slice(0, qIdx);
    } else if (parseQueryString) {
      out.query = {};
    }
    out.pathname = rest || null;
    out.path = (out.pathname || "") + (out.search || "") || null;
    return out;
  }

  try {
    const u = new globalThis.URL(urlStr);
    out.protocol = u.protocol;
    out.slashes = urlStr.includes("://") || null;
    out.auth = u.username ? u.username + (u.password ? ":" + u.password : "") : null;
    out.host = u.host || null;
    out.hostname = u.hostname || null;
    out.port = u.port || null;
    out.hash = u.hash || null;
    out.search = u.search || null;
    out.query = parseQueryString ? qsParse(u.search.replace(/^\?/, "")) : (u.search ? u.search.slice(1) : null);
    out.pathname = u.pathname || null;
    out.path = (u.pathname || "") + (u.search || "") || null;
    out.href = u.href;
  } catch (e) { /* leave defaults */ }
  return out;
}

export function format(urlObj) {
  if (typeof urlObj === "string") return urlObj;
  if (urlObj instanceof globalThis.URL) return urlObj.href;
  let str = "";
  if (urlObj.protocol) str += urlObj.protocol.endsWith(":") ? urlObj.protocol : urlObj.protocol + ":";
  if (urlObj.slashes || (urlObj.host || urlObj.hostname)) str += "//";
  if (urlObj.auth) str += urlObj.auth + "@";
  if (urlObj.host) str += urlObj.host;
  else if (urlObj.hostname) str += urlObj.hostname + (urlObj.port ? ":" + urlObj.port : "");
  if (urlObj.pathname) str += urlObj.pathname;
  if (urlObj.search) str += urlObj.search.startsWith("?") ? urlObj.search : "?" + urlObj.search;
  else if (urlObj.query && typeof urlObj.query === "object") {
    const q = qsStringify(urlObj.query);
    if (q) str += "?" + q;
  }
  if (urlObj.hash) str += urlObj.hash.startsWith("#") ? urlObj.hash : "#" + urlObj.hash;
  return str;
}

export function resolve(from, to) {
  try {
    return new globalThis.URL(to, from.includes("://") ? from : "http://localhost" + (from.startsWith("/") ? from : "/" + from)).href
      .replace(/^http:\/\/localhost/, "");
  } catch (e) {
    return to;
  }
}

export function fileURLToPath(u) {
  const s = typeof u === "string" ? u : u.href;
  return s.replace(/^file:\/\//, "") || "/";
}
export function pathToFileURL(p) {
  return new globalThis.URL("file://" + (p.startsWith("/") ? p : "/" + p));
}

function qsParse(s) {
  const obj = {};
  if (!s) return obj;
  for (const pair of s.split("&")) {
    if (!pair) continue;
    const i = pair.indexOf("=");
    const k = decodeURIComponent(i === -1 ? pair : pair.slice(0, i));
    const v = i === -1 ? "" : decodeURIComponent(pair.slice(i + 1));
    if (k in obj) { if (Array.isArray(obj[k])) obj[k].push(v); else obj[k] = [obj[k], v]; }
    else obj[k] = v;
  }
  return obj;
}
function qsStringify(obj) {
  return Object.keys(obj).map((k) => encodeURIComponent(k) + "=" + encodeURIComponent(obj[k])).join("&");
}

export default { URL, URLSearchParams, parse, format, resolve, fileURLToPath, pathToFileURL, Url: function Url() {} };
