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

  // Intl — size-reduced engine builds (this StarlingMonkey) ship without the
  // Internationalization API (ICU data is large), but libraries reference it at
  // module load. Provide a minimal, locale-naive stub: formatters fall back to
  // String()/toLocaleString and collation to code-point order. Not locale-aware,
  // but lets code that touches Intl load and run.
  if (typeof g.Intl === "undefined") {
    const opts = (o) => Object.assign({ locale: "en-US", numberingSystem: "latn" }, o || {});
    g.Intl = {
      DateTimeFormat: function (l, o) { return { format: (d) => new Date(d === undefined ? Date.now() : d).toString(), formatToParts: () => [], resolvedOptions: () => opts(o) }; },
      NumberFormat: function (l, o) { return { format: (n) => String(n), formatToParts: () => [], resolvedOptions: () => opts(o) }; },
      Collator: function () { return { compare: (a, b) => (a < b ? -1 : a > b ? 1 : 0), resolvedOptions: () => opts() }; },
      PluralRules: function () { return { select: () => "other", resolvedOptions: () => opts() }; },
      ListFormat: function () { return { format: (items) => Array.from(items || []).join(", "), formatToParts: () => [] }; },
      RelativeTimeFormat: function () { return { format: (v, u) => v + " " + u, formatToParts: () => [] }; },
      Segmenter: function () { return { segment: (s) => Array.from(String(s)).map((seg) => ({ segment: seg })) }; },
      getCanonicalLocales: (l) => (Array.isArray(l) ? l.slice() : l ? [l] : []),
    };
  }

  // Timers. Two constraints shape this:
  //  - On Javy there is no timer source at all, so 0-delay callbacks run as
  //    microtasks and real delays can't wait.
  //  - On StarlingMonkey the platform timer EXISTS but may only be used during
  //    request handling — calling setTimeout during component *initialization*
  //    throws ("setTimeout can only be used during request handling, not during
  //    initialization"). Libraries that boot at init (avvio/Fastify install a
  //    per-plugin timeout guard with setTimeout) would then reject and abort boot.
  // So wrap the platform timer: use it when it works (request time), and on
  // failure (init) fall back to a microtask for ~immediate callbacks / drop long
  // delays. A boot-time timeout guard simply never fires, which is the safe
  // outcome (plugins still complete).
  (function () {
    const realSetTimeout = typeof g.setTimeout === "function" ? g.setTimeout : null;
    g.setTimeout = function (cb) {
      const args = Array.prototype.slice.call(arguments, 2);
      const delay = arguments[1];
      const call = () => cb.apply(null, args);
      if (realSetTimeout) {
        try { return realSetTimeout(call, delay); } catch (e) { /* init: timer unavailable */ }
      }
      if (!delay || delay <= 0) g.queueMicrotask(call);
      return 0;
    };
    if (typeof g.clearTimeout !== "function") g.clearTimeout = function () {};

    const realSetInterval = typeof g.setInterval === "function" ? g.setInterval : null;
    g.setInterval = function (cb) {
      const args = Array.prototype.slice.call(arguments, 2);
      const delay = arguments[1];
      if (realSetInterval) {
        try { return realSetInterval(() => cb.apply(null, args), delay); } catch (e) {}
      }
      return 0; // no interval source during init
    };
    if (typeof g.clearInterval !== "function") g.clearInterval = function () {};
  })();
  if (typeof g.setImmediate === "undefined") {
    g.setImmediate = function (cb) {
      const args = Array.prototype.slice.call(arguments, 1);
      g.queueMicrotask(() => cb.apply(null, args));
      return 0;
    };
    g.clearImmediate = function () {};
  }

  if (typeof g.process === "undefined") {
    // hrtime over the best available clock; returns [seconds, nanoseconds] like
    // Node (used by loggers/timers, e.g. Fastify's pino child logger).
    const clock = function () {
      return typeof performance !== "undefined" && performance.now ? performance.now() : Date.now();
    };
    const hrtime = function (prev) {
      const ms = clock();
      let sec = Math.floor(ms / 1000);
      let nano = Math.floor((ms - sec * 1000) * 1e6);
      if (prev) {
        sec -= prev[0];
        nano -= prev[1];
        if (nano < 0) { sec -= 1; nano += 1e9; }
      }
      return [sec, nano];
    };
    hrtime.bigint = function () { return BigInt(Math.round(clock() * 1e6)); };

    g.process = {
      env: {},
      argv: ["node", "app"],
      execPath: "/usr/bin/node",
      platform: "linux",
      arch: "wasm32",
      version: "v18.0.0",
      versions: { node: "18.0.0" },
      pid: 1,
      ppid: 0,
      features: {},
      title: "taubyte",
      cwd: function () { return "/"; },
      chdir: function () {},
      umask: function () { return 0; },
      hrtime: hrtime,
      uptime: function () { return clock() / 1000; },
      memoryUsage: function () { return { rss: 0, heapTotal: 0, heapUsed: 0, external: 0, arrayBuffers: 0 }; },
      nextTick: function (cb) {
        const args = Array.prototype.slice.call(arguments, 1);
        g.queueMicrotask(() => cb.apply(null, args));
      },
      exit: function () {},
      // EventEmitter surface. on/emit are inert (there are no real process
      // signals here), but the methods must exist and be chainable: frameworks
      // (Fastify) register/remove signal + warning listeners during boot.
      on: function () { return g.process; },
      once: function () { return g.process; },
      off: function () { return g.process; },
      addListener: function () { return g.process; },
      removeListener: function () { return g.process; },
      prependListener: function () { return g.process; },
      prependOnceListener: function () { return g.process; },
      removeAllListeners: function () { return g.process; },
      listeners: function () { return []; },
      rawListeners: function () { return []; },
      listenerCount: function () { return 0; },
      setMaxListeners: function () { return g.process; },
      eventNames: function () { return []; },
      emit: function () { return false; },
      // emitWarning must exist: process-warning (Fastify, fastify deps) calls it
      // unguarded when emitting deprecation/feature warnings during boot.
      emitWarning: function () {},
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
      constructor(type) { this.type = type; }
      runInAsyncScope(fn, thisArg, ...args) { return fn.apply(thisArg, args); }
      // No async-id tracking here; these exist so libraries (Fastify wraps each
      // request in an AsyncResource and calls emitDestroy on completion) don't
      // trip over a missing method. All no-ops returning this.
      emitDestroy() { return this; }
      emitBefore() { return this; }
      emitAfter() { return this; }
      asyncId() { return 0; }
      triggerAsyncId() { return 0; }
      bind(fn) { return fn; }
      static bind(fn) { return fn; }
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
      toString(enc, start, end) {
        const v = start || end ? this.subarray(start || 0, end == null ? this.length : end) : this;
        if (enc === "base64" || enc === "base64url") return bufToB64(v);
        if (enc === "hex") return bufToHex(v);
        if (enc === "latin1" || enc === "binary" || enc === "ascii") { let s = ""; for (const b of v) s += String.fromCharCode(enc === "ascii" ? b & 0x7f : b); return s; }
        return new TextDecoder().decode(v);
      }
      // Buffer views share memory (like Node), not copy (unlike Uint8Array.slice).
      subarray(start, end) {
        const u = Uint8Array.prototype.subarray.call(this, start, end);
        return new Buffer(u.buffer, u.byteOffset, u.length);
      }
      slice(start, end) { return this.subarray(start, end); }
      copy(target, ts, ss, se) {
        ts = ts || 0; ss = ss || 0; se = se == null ? this.length : se;
        const sub = this.subarray(ss, se);
        target.set(sub.subarray(0, Math.min(sub.length, target.length - ts)), ts);
        return Math.min(sub.length, target.length - ts);
      }
      equals(other) {
        if (!other || other.length !== this.length) return false;
        for (let i = 0; i < this.length; i++) if (this[i] !== other[i]) return false;
        return true;
      }
      write(string, offset, length, encoding) {
        if (typeof offset === "string") { encoding = offset; offset = 0; length = undefined; }
        else if (typeof length === "string") { encoding = length; length = undefined; }
        offset = offset || 0;
        const bytes = encoding === "hex" ? hexToBuf(string)
          : encoding === "base64" ? b64ToBuf(string)
          : new TextEncoder().encode(String(string));
        const n = length == null ? bytes.length : Math.min(length, bytes.length);
        this.set(bytes.subarray(0, Math.min(n, this.length - offset)), offset);
        return Math.min(n, this.length - offset);
      }
      // Integer reads/writes (used by hashing/binary code, e.g. writeUInt32BE).
      readUInt8(o) { o = o || 0; return this[o]; }
      readInt8(o) { o = o || 0; const v = this[o]; return v & 0x80 ? v - 0x100 : v; }
      readUInt16BE(o) { o = o || 0; return (this[o] << 8) | this[o + 1]; }
      readUInt16LE(o) { o = o || 0; return (this[o + 1] << 8) | this[o]; }
      readInt16BE(o) { const v = this.readUInt16BE(o); return v & 0x8000 ? v - 0x10000 : v; }
      readInt16LE(o) { const v = this.readUInt16LE(o); return v & 0x8000 ? v - 0x10000 : v; }
      readUInt32BE(o) { o = o || 0; return (this[o] * 0x1000000) + (this[o + 1] << 16) + (this[o + 2] << 8) + this[o + 3]; }
      readUInt32LE(o) { o = o || 0; return this[o] + (this[o + 1] << 8) + (this[o + 2] << 16) + (this[o + 3] * 0x1000000); }
      readInt32BE(o) { o = o || 0; return (this[o] << 24) | (this[o + 1] << 16) | (this[o + 2] << 8) | this[o + 3]; }
      readInt32LE(o) { o = o || 0; return (this[o + 3] << 24) | (this[o + 2] << 16) | (this[o + 1] << 8) | this[o]; }
      writeUInt8(v, o) { o = o || 0; this[o] = v & 0xff; return o + 1; }
      writeInt8(v, o) { return this.writeUInt8(v, o); }
      writeUInt16BE(v, o) { o = o || 0; this[o] = (v >>> 8) & 0xff; this[o + 1] = v & 0xff; return o + 2; }
      writeUInt16LE(v, o) { o = o || 0; this[o] = v & 0xff; this[o + 1] = (v >>> 8) & 0xff; return o + 2; }
      writeInt16BE(v, o) { return this.writeUInt16BE(v, o); }
      writeInt16LE(v, o) { return this.writeUInt16LE(v, o); }
      writeUInt32BE(v, o) { o = o || 0; this[o] = (v >>> 24) & 0xff; this[o + 1] = (v >>> 16) & 0xff; this[o + 2] = (v >>> 8) & 0xff; this[o + 3] = v & 0xff; return o + 4; }
      writeUInt32LE(v, o) { o = o || 0; this[o] = v & 0xff; this[o + 1] = (v >>> 8) & 0xff; this[o + 2] = (v >>> 16) & 0xff; this[o + 3] = (v >>> 24) & 0xff; return o + 4; }
      writeInt32BE(v, o) { return this.writeUInt32BE(v, o); }
      writeInt32LE(v, o) { return this.writeUInt32LE(v, o); }
    }
    g.Buffer = Buffer;

    // safe-buffer / safer-buffer (pulled in by iconv-lite, used by body parsers)
    // copy Buffer's static methods with a `for..in` loop, which only sees
    // ENUMERABLE properties. ES6 `static` methods are non-enumerable, so those
    // copies silently drop isBuffer/from/etc. (safer-buffer has no isBuffer
    // fallback -> "Buffer.isBuffer is not a function"). Expose the statics as
    // enumerable so the copies pick them all up.
    for (const k of ["from", "alloc", "allocUnsafe", "allocUnsafeSlow", "isBuffer", "concat", "byteLength"]) {
      Object.defineProperty(Buffer, k, { enumerable: true });
    }
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

  // Randomness during component *initialization*. componentize-js snapshots the
  // module's top-level with Wizer, during which imported host functions —
  // including wasi:random — can't be called: an app/framework that generates
  // random values or UUIDs at init/boot time (e.g. Fastify's avvio boot) would
  // hard-trap ("attempted to call wasi:random during Wizer initialization").
  // Gate the secure source on a "serving" flag the request bridges set: during
  // init fall back to a fast non-secure PRNG (init-time random is for ids/seeds,
  // not security); at request time use the real WebCrypto. Math.random is never
  // security-bearing, so always route it through the PRNG (also avoids wasi:random
  // seeding at init).
  (function () {
    let s0 = (Date.now() >>> 0) ^ 0x9e3779b9, s1 = 0x243f6a88;
    function prng() {
      s1 ^= s1 << 13; s1 ^= s1 >>> 17; s1 ^= s1 << 5; s1 |= 0;
      s0 = (s0 + 0x6d2b79f5) | 0;
      let t = Math.imul(s0 ^ (s0 >>> 15), 1 | s0) ^ s1;
      return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
    }
    function fill(arr) {
      const u8 = arr instanceof Uint8Array ? arr : new Uint8Array(arr.buffer || arr.length || 0);
      for (let i = 0; i < u8.length; i++) u8[i] = (prng() * 256) & 255;
      return arr;
    }
    function prngUUID() {
      const b = new Uint8Array(16); fill(b);
      b[6] = (b[6] & 0x0f) | 0x40; b[8] = (b[8] & 0x3f) | 0x80;
      let h = ""; for (let i = 0; i < 16; i++) h += b[i].toString(16).padStart(2, "0");
      return h.slice(0, 8) + "-" + h.slice(8, 12) + "-" + h.slice(12, 16) + "-" + h.slice(16, 20) + "-" + h.slice(20);
    }
    const serving = function () { return !!g.__TAUBYTE_SERVING; };

    // Math.random: pure JS (never hits wasi:random).
    try { g.Math.random = prng; } catch (e) {}

    const rc = g.crypto;
    if (rc) {
      const realGRV = typeof rc.getRandomValues === "function" ? rc.getRandomValues.bind(rc) : null;
      const realUUID = typeof rc.randomUUID === "function" ? rc.randomUUID.bind(rc) : null;
      const getRandomValues = function (arr) { return serving() && realGRV ? realGRV(arr) : fill(arr); };
      const randomUUID = function () { return serving() && realUUID ? realUUID() : prngUUID(); };
      try {
        rc.getRandomValues = getRandomValues;
        rc.randomUUID = randomUUID;
      } catch (e) {
        try { g.crypto = { getRandomValues, randomUUID, subtle: rc.subtle }; } catch (e2) {}
      }
    }
  })();
})(globalThis);
