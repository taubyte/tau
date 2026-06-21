// Minimal Web API polyfill for Javy/QuickJS, which ships only ES + console +
// TextEncoder/TextDecoder. Provides URL, URLSearchParams, Headers, Request and
// Response on globalThis so Web-standard frameworks (Hono, Remix, SvelteKit,
// Next's edge handler) can run.
//
// PROTOTYPE: this targets the common SSR path — methods, headers, text/json
// bodies, URL parsing — not full WHATWG conformance. Validate + iterate with
// real apps; extend as needed (streams, fetch, FormData).

(function (g) {
  // define sets an own, writable data property — used for Request/Response
  // fields so a subclass that declares them as getters (Next.js' NextRequest
  // does `get url()`) doesn't make our constructor's assignment throw
  // "no setter for property".
  function define(o, k, v) {
    Object.defineProperty(o, k, { value: v, writable: true, configurable: true, enumerable: true });
  }

  if (typeof g.URLSearchParams === "undefined") {
    g.URLSearchParams = class URLSearchParams {
      constructor(init) {
        this._ = [];
        if (typeof init === "string") {
          init = init.replace(/^\?/, "");
          if (init)
            for (const pair of init.split("&")) {
              const i = pair.indexOf("=");
              const k = dec(i < 0 ? pair : pair.slice(0, i));
              const v = i < 0 ? "" : dec(pair.slice(i + 1));
              this._.push([k, v]);
            }
        } else if (init && typeof init === "object") {
          for (const k in init) this._.push([k, String(init[k])]);
        }
      }
      get(k) { for (const e of this._) if (e[0] === k) return e[1]; return null; }
      getAll(k) { return this._.filter((e) => e[0] === k).map((e) => e[1]); }
      has(k) { return this._.some((e) => e[0] === k); }
      set(k, v) { this.delete(k); this._.push([k, String(v)]); }
      append(k, v) { this._.push([k, String(v)]); }
      delete(k) { this._ = this._.filter((e) => e[0] !== k); }
      forEach(cb) { for (const e of this._) cb(e[1], e[0], this); }
      entries() { return this._.map((e) => [e[0], e[1]])[Symbol.iterator](); }
      keys() { return this._.map((e) => e[0])[Symbol.iterator](); }
      values() { return this._.map((e) => e[1])[Symbol.iterator](); }
      toString() { return this._.map((e) => enc(e[0]) + "=" + enc(e[1])).join("&"); }
      [Symbol.iterator]() { return this.entries(); }
    };
    function dec(s) { try { return decodeURIComponent(s.replace(/\+/g, " ")); } catch { return s; } }
    function enc(s) { return encodeURIComponent(s); }
  }

  if (typeof g.URL === "undefined") {
    const SCHEME = /^[a-zA-Z][a-zA-Z0-9+.-]*:/;
    // scheme, optional //authority, path, ?query, #hash. The authority is
    // optional so opaque URLs (a:, mailto:x, data:...) parse instead of throwing
    // — SvelteKit constructs `new URL("a:")` as a placeholder during SSR.
    const RE = /^([a-zA-Z][a-zA-Z0-9+.-]*:)(\/\/([^/?#]*))?([^?#]*)(\?[^#]*)?(#.*)?$/;
    g.URL = class URL {
      constructor(url, base) {
        let s = String(url);
        if (!SCHEME.test(s)) {
          if (base == null) throw new TypeError("Invalid URL: " + url);
          const b = new g.URL(base);
          if (s.startsWith("//")) s = b.protocol + s;
          else if (s.startsWith("/")) s = b.origin + s;
          else if (s.startsWith("?")) s = b.origin + b.pathname + s;
          else if (s.startsWith("#")) s = b.origin + b.pathname + b.search + s;
          else s = b.origin + b.pathname.replace(/[^/]*$/, "") + s;
        }
        const m = RE.exec(s);
        if (!m) throw new TypeError("Invalid URL: " + url);
        this.protocol = m[1];
        const hasAuthority = m[2] !== undefined;
        this.host = m[3] || "";
        const at = this.host.indexOf("@");
        const hostport = at >= 0 ? this.host.slice(at + 1) : this.host;
        this.hostname = hostport.split(":")[0];
        this.port = hostport.split(":")[1] || "";
        // With an authority an empty path normalizes to "/"; an opaque URL keeps
        // its (possibly empty) path and has a null origin.
        this.pathname = m[4] || (hasAuthority ? "/" : "");
        this.search = m[5] || "";
        this.hash = m[6] || "";
        this.searchParams = new g.URLSearchParams(this.search);
        this.origin = hasAuthority ? this.protocol + "//" + this.host : "null";
      }
      get href() {
        const q = this.searchParams.toString();
        const base = this.origin !== "null" ? this.origin : this.protocol;
        return base + this.pathname + (q ? "?" + q : "") + this.hash;
      }
      toString() { return this.href; }
    };
  }

  if (typeof g.Headers === "undefined") {
    g.Headers = class Headers {
      constructor(init) {
        this._ = new Map();
        if (init) {
          if (init instanceof g.Headers) init.forEach((v, k) => this.append(k, v));
          else if (Array.isArray(init)) init.forEach((p) => this.append(p[0], p[1]));
          else if (typeof init.forEach === "function") init.forEach((v, k) => this.append(k, v));
          else for (const k in init) this.append(k, init[k]);
        }
      }
      append(k, v) { k = k.toLowerCase(); const e = this._.get(k); this._.set(k, e ? e + ", " + v : String(v)); }
      set(k, v) { this._.set(k.toLowerCase(), String(v)); }
      get(k) { const v = this._.get(k.toLowerCase()); return v === undefined ? null : v; }
      has(k) { return this._.has(k.toLowerCase()); }
      delete(k) { this._.delete(k.toLowerCase()); }
      forEach(cb) { this._.forEach((v, k) => cb(v, k, this)); }
      entries() { return this._.entries(); }
      keys() { return this._.keys(); }
      values() { return this._.values(); }
      [Symbol.iterator]() { return this._.entries(); }
    };
  }

  const body = {
    text() {
      const b = this._body;
      if (b == null) return Promise.resolve("");
      if (typeof b === "string") return Promise.resolve(b);
      if (b instanceof Uint8Array) return Promise.resolve(new TextDecoder().decode(b));
      if (b instanceof ArrayBuffer) return Promise.resolve(new TextDecoder().decode(new Uint8Array(b)));
      return Promise.resolve(String(b));
    },
    json() { return this.text().then((t) => JSON.parse(t || "null")); },
    arrayBuffer() { return this.text().then((t) => new TextEncoder().encode(t).buffer); },
  };

  if (typeof g.Request === "undefined") {
    g.Request = class Request {
      constructor(input, init) {
        init = init || {};
        // input may be a string, a URL (has .href), or another Request (.url).
        // Use define() so a Request subclass with getter-only fields is safe.
        define(this, "url",
          typeof input === "string"
            ? input
            : (input && (input.href != null ? input.href : input.url)) || "");
        define(this, "method", String(init.method || (input && input.method) || "GET").toUpperCase());
        define(this, "headers", new g.Headers(init.headers || (input && input.headers)));
        this._body = init.body != null ? init.body : (input && input._body) || null;
      }
      // .body is consumed by clone patterns like `new Response(res.body, res)`;
      // expose the raw body rather than a ReadableStream (not implemented). A
      // setter is provided because edge runtimes assign req.body.
      get body() { return this._body; }
      set body(v) { this._body = v; }
      clone() { return new g.Request(this.url, { method: this.method, headers: this.headers, body: this._body }); }
    };
    Object.assign(g.Request.prototype, body);
  }

  if (typeof g.Response === "undefined") {
    g.Response = class Response {
      constructor(b, init) {
        init = init || {};
        this._body = b != null ? b : null;
        // define() so a Response subclass (NextResponse) with getter-only fields
        // is safe against these assignments.
        define(this, "status", init.status || 200);
        define(this, "statusText", init.statusText || "");
        define(this, "headers", new g.Headers(init.headers));
        define(this, "ok", (init.status || 200) >= 200 && (init.status || 200) < 300);
      }
      static json(data, init) {
        const r = new g.Response(JSON.stringify(data), init);
        if (!r.headers.has("content-type")) r.headers.set("content-type", "application/json");
        return r;
      }
      static redirect(url, status) {
        const r = new g.Response(null, { status: status || 302 });
        r.headers.set("location", url);
        return r;
      }
      // Raw body for clone patterns like `new Response(res.body, res)` (used by
      // next-on-pages); a real ReadableStream is not implemented. A setter is
      // provided because edge runtimes assign res.body.
      get body() { return this._body; }
      set body(v) { this._body = v; }
      clone() { return new g.Response(this._body, { status: this.status, headers: this.headers }); }
    };
    Object.assign(g.Response.prototype, body);
  }

  if (typeof g.fetch === "undefined") {
    // Outbound fetch is not wired yet; route through Taubyte primitives later.
    g.fetch = function () {
      return Promise.reject(new Error("fetch is not available in this runtime yet"));
    };
  }

  // base64 (binary strings), commonly used by framework runtimes.
  if (typeof g.btoa === "undefined") {
    const B = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
    g.btoa = function (s) {
      s = String(s);
      let o = "";
      for (let i = 0; i < s.length; i += 3) {
        const a = s.charCodeAt(i), b = i + 1 < s.length ? s.charCodeAt(i + 1) : 0, c = i + 2 < s.length ? s.charCodeAt(i + 2) : 0;
        o += B[a >> 2] + B[((a & 3) << 4) | (b >> 4)];
        o += i + 1 < s.length ? B[((b & 15) << 2) | (c >> 6)] : "=";
        o += i + 2 < s.length ? B[c & 63] : "=";
      }
      return o;
    };
    g.atob = function (s) {
      s = String(s).replace(/[^A-Za-z0-9+/]/g, "");
      let o = "";
      for (let i = 0; i < s.length; i += 4) {
        const n = (B.indexOf(s[i]) << 18) | (B.indexOf(s[i + 1]) << 12) | ((B.indexOf(s[i + 2]) & 63) << 6) | (B.indexOf(s[i + 3]) & 63);
        o += String.fromCharCode((n >> 16) & 255);
        if (s[i + 2] !== undefined) o += String.fromCharCode((n >> 8) & 255);
        if (s[i + 3] !== undefined) o += String.fromCharCode(n & 255);
      }
      return o;
    };
  }

  // structuredClone (deep clone; JSON fallback — no cycles/functions/blobs).
  if (typeof g.structuredClone === "undefined") {
    g.structuredClone = function (v) { return v == null ? v : JSON.parse(JSON.stringify(v)); };
  }

  // crypto: WARNING — Math.random based, NOT cryptographically secure. Provided
  // so frameworks that call randomUUID/getRandomValues for non-security ids
  // (cache keys, request ids) don't crash on Javy. Real WebCrypto arrives with
  // the component-model engine (see docs/js-runtime-roadmap.md). Do not rely on
  // this for security.
  if (typeof g.crypto === "undefined") g.crypto = {};
  if (typeof g.crypto.getRandomValues === "undefined") {
    g.crypto.getRandomValues = function (arr) {
      for (let i = 0; i < arr.length; i++) arr[i] = (Math.random() * 256) | 0;
      return arr;
    };
  }
  if (typeof g.crypto.randomUUID === "undefined") {
    g.crypto.randomUUID = function () {
      return "xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx".replace(/[xy]/g, function (c) {
        const r = (Math.random() * 16) | 0;
        return (c === "x" ? r : (r & 0x3) | 0x8).toString(16);
      });
    };
  }

  if (typeof g.performance === "undefined") {
    const t0 = Date.now();
    g.performance = { now: function () { return Date.now() - t0; }, timeOrigin: t0 };
  }

  // Cloudflare Cache API (caches.default / caches.open(name)). Taubyte has no
  // edge cache, so this is a no-op store that always misses: frameworks check
  // the cache, miss, render fresh, then "store" into a sink. Correct semantics,
  // just uncached. SvelteKit's adapter-cloudflare reads caches.default.
  if (typeof g.caches === "undefined") {
    const noopCache = {
      match() { return Promise.resolve(undefined); },
      matchAll() { return Promise.resolve([]); },
      add() { return Promise.resolve(); },
      addAll() { return Promise.resolve(); },
      put() { return Promise.resolve(); },
      delete() { return Promise.resolve(false); },
      keys() { return Promise.resolve([]); },
    };
    g.caches = {
      default: noopCache,
      open() { return Promise.resolve(noopCache); },
      match() { return Promise.resolve(undefined); },
      has() { return Promise.resolve(false); },
      delete() { return Promise.resolve(false); },
      keys() { return Promise.resolve([]); },
    };
  }

  if (typeof g.AbortController === "undefined") {
    g.AbortSignal = class AbortSignal {
      constructor() { this.aborted = false; this.reason = undefined; this._cbs = []; }
      addEventListener(t, cb) { if (t === "abort") this._cbs.push(cb); }
      removeEventListener(t, cb) { this._cbs = this._cbs.filter((f) => f !== cb); }
      dispatchEvent() {}
      throwIfAborted() { if (this.aborted) throw this.reason || new Error("aborted"); }
    };
    g.AbortController = class AbortController {
      constructor() { this.signal = new g.AbortSignal(); }
      abort(reason) {
        if (this.signal.aborted) return;
        this.signal.aborted = true;
        this.signal.reason = reason || new Error("aborted");
        for (const cb of this.signal._cbs) { try { cb({ type: "abort" }); } catch (e) {} }
      }
    };
  }

  if (typeof g.Blob === "undefined") {
    g.Blob = class Blob {
      constructor(parts, opts) {
        this._text = (parts || []).map((p) => (typeof p === "string" ? p : p && p._body != null ? String(p._body) : String(p))).join("");
        this.size = new TextEncoder().encode(this._text).length;
        this.type = (opts && opts.type) || "";
      }
      text() { return Promise.resolve(this._text); }
      arrayBuffer() { return Promise.resolve(new TextEncoder().encode(this._text).buffer); }
    };
  }

  // FormData + Request.formData() for urlencoded bodies (form actions).
  // multipart/form-data is not parsed yet.
  if (typeof g.FormData === "undefined") {
    g.FormData = class FormData {
      constructor() { this._ = []; }
      append(k, v) { this._.push([k, v]); }
      set(k, v) { this.delete(k); this._.push([k, v]); }
      get(k) { for (const e of this._) if (e[0] === k) return e[1]; return null; }
      getAll(k) { return this._.filter((e) => e[0] === k).map((e) => e[1]); }
      has(k) { return this._.some((e) => e[0] === k); }
      delete(k) { this._ = this._.filter((e) => e[0] !== k); }
      forEach(cb) { for (const e of this._) cb(e[1], e[0], this); }
      entries() { return this._.map((e) => [e[0], e[1]])[Symbol.iterator](); }
      keys() { return this._.map((e) => e[0])[Symbol.iterator](); }
      values() { return this._.map((e) => e[1])[Symbol.iterator](); }
      [Symbol.iterator]() { return this.entries(); }
    };
    if (g.Request && !g.Request.prototype.formData) {
      g.Request.prototype.formData = function () {
        return this.text().then((t) => {
          const fd = new g.FormData();
          const ct = (this.headers && this.headers.get && this.headers.get("content-type")) || "";
          if (ct.indexOf("application/x-www-form-urlencoded") >= 0) {
            for (const pair of t.split("&")) {
              if (!pair) continue;
              const i = pair.indexOf("=");
              const k = decodeURIComponent((i < 0 ? pair : pair.slice(0, i)).replace(/\+/g, " "));
              const v = i < 0 ? "" : decodeURIComponent(pair.slice(i + 1).replace(/\+/g, " "));
              fd.append(k, v);
            }
          }
          return fd;
        });
      };
    }
  }
})(globalThis);
