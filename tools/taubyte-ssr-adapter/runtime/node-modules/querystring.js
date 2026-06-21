// node:querystring shim. The legacy query-string API, implemented over the
// platform's encodeURIComponent/decodeURIComponent (StarlingMonkey-native).

export function escape(str) {
  return encodeURIComponent(str);
}
export function unescape(str) {
  try { return decodeURIComponent(str); } catch (e) { return str; }
}

export function parse(qs, sep = "&", eq = "=", options) {
  const obj = {};
  if (typeof qs !== "string" || qs.length === 0) return obj;
  const maxKeys = options && typeof options.maxKeys === "number" ? options.maxKeys : 1000;
  const pairs = qs.split(sep);
  const limit = maxKeys > 0 ? Math.min(pairs.length, maxKeys) : pairs.length;
  for (let i = 0; i < limit; i++) {
    const pair = pairs[i];
    if (pair.length === 0) continue;
    const idx = pair.indexOf(eq);
    let k, v;
    if (idx === -1) { k = unescape(pair); v = ""; }
    else { k = unescape(pair.slice(0, idx)); v = unescape(pair.slice(idx + eq.length)); }
    if (Object.prototype.hasOwnProperty.call(obj, k)) {
      const cur = obj[k];
      if (Array.isArray(cur)) cur.push(v);
      else obj[k] = [cur, v];
    } else obj[k] = v;
  }
  return obj;
}

export function stringify(obj, sep = "&", eq = "=") {
  if (obj == null || typeof obj !== "object") return "";
  const out = [];
  for (const k of Object.keys(obj)) {
    const ek = escape(k);
    const v = obj[k];
    if (Array.isArray(v)) {
      for (const item of v) out.push(ek + eq + escape(stringifyPrimitive(item)));
    } else {
      out.push(ek + eq + escape(stringifyPrimitive(v)));
    }
  }
  return out.join(sep);
}

function stringifyPrimitive(v) {
  if (typeof v === "string") return v;
  if (typeof v === "number" && isFinite(v)) return "" + v;
  if (typeof v === "boolean") return v ? "true" : "false";
  return "";
}

export const decode = parse;
export const encode = stringify;

export default { parse, stringify, escape, unescape, decode, encode };
