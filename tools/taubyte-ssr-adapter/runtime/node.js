// Minimal Node-compat shims for Javy/QuickJS, layered on top of the Web API
// polyfill (web.js). Targets the slice of Node that edge-runtime code (Next's
// edge handler, some framework internals) touches — not full Node.
//
// PROTOTYPE: process/Buffer/global/timers/queueMicrotask only. fs/net/streams
// are intentionally absent (a WASM sandbox has no ambient filesystem or
// sockets); route data through Taubyte primitives instead. Validate + iterate.

(function (g) {
  if (typeof g.global === "undefined") g.global = g;
  if (typeof g.globalThis === "undefined") g.globalThis = g;

  // queueMicrotask via the (event-loop-enabled) promise job queue.
  if (typeof g.queueMicrotask === "undefined") {
    g.queueMicrotask = function (cb) { Promise.resolve().then(cb); };
  }

  // Timers: there is no real timer source, so 0-delay callbacks run as
  // microtasks and delays are best-effort ignored. Enough for setTimeout(fn, 0)
  // patterns; long delays do not actually wait.
  if (typeof g.setTimeout === "undefined") {
    g.setTimeout = function (cb) { g.queueMicrotask(() => cb()); return 0; };
    g.clearTimeout = function () {};
  }
  if (typeof g.setImmediate === "undefined") {
    g.setImmediate = function (cb) { g.queueMicrotask(() => cb()); return 0; };
    g.clearImmediate = function () {};
  }

  if (typeof g.process === "undefined") {
    g.process = {
      env: {},
      argv: ["javy", "app"],
      platform: "wasi",
      arch: "wasm",
      version: "v18.0.0",
      versions: { node: "18.0.0" },
      pid: 1,
      cwd: function () { return "/"; },
      nextTick: function (cb) {
        const args = Array.prototype.slice.call(arguments, 1);
        g.queueMicrotask(() => cb.apply(null, args));
      },
      exit: function () {},
      on: function () {},
      once: function () {},
      off: function () {},
      emit: function () { return false; },
    };
  }

  // AsyncLocalStorage on the global. Next.js' edge runtime captures
  // globalThis.AsyncLocalStorage at module-evaluation time and falls back to a
  // throwing stub if it's missing, so it must exist before route modules load
  // (node.js runs in the prelude, ahead of them). Single-flow only — see the
  // node:async_hooks shim, which re-exports this same class.
  if (typeof g.AsyncLocalStorage === "undefined") {
    g.AsyncLocalStorage = class AsyncLocalStorage {
      run(store, cb, ...args) { this._store = store; return cb(...args); }
      getStore() { return this._store; }
      enterWith(store) { this._store = store; }
      exit(cb, ...args) {
        const prev = this._store;
        this._store = undefined;
        try { return cb(...args); } finally { this._store = prev; }
      }
      disable() { this._store = undefined; }
    };
    g.AsyncResource = class AsyncResource {
      constructor() {}
      runInAsyncScope(fn, thisArg, ...args) { return fn.apply(thisArg, args); }
      bind(fn) { return fn; }
    };
  }

  // Minimal Buffer over Uint8Array (utf8 + base64/hex). Not spec-complete.
  if (typeof g.Buffer === "undefined") {
    class Buffer extends Uint8Array {
      static from(input, enc) {
        if (typeof input === "string") {
          if (enc === "base64") return b64ToBuf(input);
          if (enc === "hex") return hexToBuf(input);
          return new Buffer(new TextEncoder().encode(input));
        }
        if (input instanceof Uint8Array || Array.isArray(input)) return new Buffer(input);
        if (input instanceof ArrayBuffer) return new Buffer(new Uint8Array(input));
        return new Buffer(0);
      }
      static alloc(n) { return new Buffer(n); }
      // safe-buffer/safer-buffer treat a Buffer as "complete native" only when
      // from/alloc/allocUnsafe/allocUnsafeSlow all exist, otherwise they wrap it
      // in a shim that copies only enumerable props — dropping static methods
      // like isBuffer. Provide all four so they re-export ours directly.
      static allocUnsafe(n) { return new Buffer(n); }
      static allocUnsafeSlow(n) { return new Buffer(n); }
      static isBuffer(b) { return b instanceof Buffer; }
      static concat(list) {
        let len = 0;
        for (const b of list) len += b.length;
        const out = new Buffer(len);
        let off = 0;
        for (const b of list) { out.set(b, off); off += b.length; }
        return out;
      }
      static byteLength(s) { return new TextEncoder().encode(String(s)).length; }
      toString(enc) {
        if (enc === "base64") return bufToB64(this);
        if (enc === "hex") return bufToHex(this);
        return new TextDecoder().decode(this);
      }
    }
    g.Buffer = Buffer;

    const B64 = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
    function bufToB64(buf) {
      let out = "";
      for (let i = 0; i < buf.length; i += 3) {
        const a = buf[i], b = buf[i + 1], c = buf[i + 2];
        out += B64[a >> 2] + B64[((a & 3) << 4) | (b >> 4)];
        out += i + 1 < buf.length ? B64[((b & 15) << 2) | (c >> 6)] : "=";
        out += i + 2 < buf.length ? B64[c & 63] : "=";
      }
      return out;
    }
    function b64ToBuf(s) {
      s = s.replace(/[^A-Za-z0-9+/]/g, "");
      const out = [];
      for (let i = 0; i < s.length; i += 4) {
        const n = (B64.indexOf(s[i]) << 18) | (B64.indexOf(s[i + 1]) << 12) | (B64.indexOf(s[i + 2]) << 6) | B64.indexOf(s[i + 3]);
        out.push((n >> 16) & 255);
        if (s[i + 2] !== undefined) out.push((n >> 8) & 255);
        if (s[i + 3] !== undefined) out.push(n & 255);
      }
      return new g.Buffer(out);
    }
    function bufToHex(buf) {
      let out = "";
      for (const x of buf) out += x.toString(16).padStart(2, "0");
      return out;
    }
    function hexToBuf(s) {
      const out = [];
      for (let i = 0; i < s.length; i += 2) out.push(parseInt(s.substr(i, 2), 16));
      return new g.Buffer(out);
    }
  }
})(globalThis);
