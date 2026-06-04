// node:util shim — the slice the npm ecosystem reaches for (inherits, format,
// inspect, promisify, deprecate, debuglog, types, TextEncoder/Decoder). Not
// spec-complete; covers what Express and its deps actually call.

export function inherits(ctor, superCtor) {
  if (superCtor) {
    ctor.super_ = superCtor;
    Object.setPrototypeOf(ctor.prototype, superCtor.prototype);
  }
}

export function format(f, ...args) {
  if (typeof f !== "string") {
    return [f, ...args].map((a) => (typeof a === "string" ? a : inspect(a))).join(" ");
  }
  let i = 0;
  let str = f.replace(/%[sdifjoO%]/g, (m) => {
    if (m === "%%") return "%";
    if (i >= args.length) return m;
    const a = args[i++];
    switch (m) {
      case "%s": return typeof a === "string" ? a : inspect(a);
      case "%d":
      case "%i": return String(parseInt(a, 10));
      case "%f": return String(parseFloat(a));
      case "%j": try { return JSON.stringify(a); } catch (e) { return "[Circular]"; }
      default: return inspect(a);
    }
  });
  for (; i < args.length; i++) {
    const a = args[i];
    str += " " + (typeof a === "string" ? a : inspect(a));
  }
  return str;
}

export function inspect(obj, opts) {
  const seen = new Set();
  const depth = opts && typeof opts.depth === "number" ? opts.depth : 2;
  const walk = (v, d) => {
    if (v === null) return "null";
    const t = typeof v;
    if (t === "string") return d === 0 ? v : "'" + v + "'";
    if (t === "number" || t === "boolean" || t === "undefined" || t === "bigint") return String(v);
    if (t === "function") return "[Function: " + (v.name || "anonymous") + "]";
    if (t === "symbol") return v.toString();
    if (v instanceof Error) return v.stack || v.toString();
    if (seen.has(v)) return "[Circular]";
    if (d > depth) return Array.isArray(v) ? "[Array]" : "[Object]";
    seen.add(v);
    try {
      if (Array.isArray(v)) return "[ " + v.map((x) => walk(x, d + 1)).join(", ") + " ]";
      const keys = Object.keys(v);
      return "{ " + keys.map((k) => k + ": " + walk(v[k], d + 1)).join(", ") + " }";
    } finally {
      seen.delete(v);
    }
  };
  return walk(obj, 0);
}
inspect.custom = Symbol.for("nodejs.util.inspect.custom");

export function promisify(fn) {
  return function (...args) {
    return new Promise((resolve, reject) => {
      fn.call(this, ...args, (err, ...rest) => {
        if (err) reject(err);
        else resolve(rest.length > 1 ? rest : rest[0]);
      });
    });
  };
}

export function callbackify(fn) {
  return function (...args) {
    const cb = args.pop();
    fn.apply(this, args).then((v) => cb(null, v), (e) => cb(e));
  };
}

export function deprecate(fn) {
  return fn; // no warnings in the sandbox
}

export function debuglog() {
  return function () {};
}

export const types = {
  isDate: (v) => v instanceof Date,
  isRegExp: (v) => v instanceof RegExp,
  isNativeError: (v) => v instanceof Error,
  isPromise: (v) => v && typeof v.then === "function",
  isMap: (v) => v instanceof Map,
  isSet: (v) => v instanceof Set,
  isAsyncFunction: (v) => typeof v === "function" && v.constructor && v.constructor.name === "AsyncFunction",
  isArrayBuffer: (v) => v instanceof ArrayBuffer,
  isTypedArray: (v) => ArrayBuffer.isView(v) && !(v instanceof DataView),
  isUint8Array: (v) => v instanceof Uint8Array,
};

// Legacy is* helpers (still used by old packages).
export const isArray = Array.isArray;
export const isBuffer = (v) => typeof Buffer !== "undefined" && Buffer.isBuffer && Buffer.isBuffer(v);
export const isDate = types.isDate;
export const isRegExp = types.isRegExp;
export const isError = types.isNativeError;
export const isFunction = (v) => typeof v === "function";
export const isObject = (v) => v !== null && typeof v === "object";
export const isString = (v) => typeof v === "string";
export const isNumber = (v) => typeof v === "number";
export const isNullOrUndefined = (v) => v == null;
export const isPrimitive = (v) => v === null || (typeof v !== "object" && typeof v !== "function");

export function _extend(target, source) {
  return Object.assign(target, source);
}

export const TextEncoder = globalThis.TextEncoder;
export const TextDecoder = globalThis.TextDecoder;

export default {
  inherits, format, inspect, promisify, callbackify, deprecate, debuglog, types,
  isArray, isBuffer, isDate, isRegExp, isError, isFunction, isObject, isString,
  isNumber, isNullOrUndefined, isPrimitive, _extend,
  TextEncoder: globalThis.TextEncoder, TextDecoder: globalThis.TextDecoder,
};
