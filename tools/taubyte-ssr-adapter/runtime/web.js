// Minimal Web API polyfill for Javy/QuickJS, which ships only ES + console +
// TextEncoder/TextDecoder. Provides URL, URLSearchParams, Headers, Request and
// Response on globalThis so Web-standard frameworks (Hono, Remix, SvelteKit,
// Next's edge handler) can run.
//
// PROTOTYPE: this targets the common SSR path — methods, headers, text/json
// bodies, URL parsing — not full WHATWG conformance. Validate + iterate with
// real apps; extend as needed (streams, fetch, FormData).

(function (g) {
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
    const RE = /^([a-zA-Z][a-zA-Z0-9+.-]*:)\/\/([^/?#]*)([^?#]*)(\?[^#]*)?(#.*)?$/;
    g.URL = class URL {
      constructor(url, base) {
        let s = String(url);
        if (base && !RE.test(s)) {
          const b = new g.URL(base);
          s = s.startsWith("/") ? b.origin + s : b.origin + b.pathname.replace(/[^/]*$/, "") + s;
        }
        const m = RE.exec(s);
        if (!m) throw new TypeError("Invalid URL: " + url);
        this.protocol = m[1];
        this.host = m[2];
        const at = this.host.indexOf("@");
        const hostport = at >= 0 ? this.host.slice(at + 1) : this.host;
        this.hostname = hostport.split(":")[0];
        this.port = hostport.split(":")[1] || "";
        this.pathname = m[3] || "/";
        this.search = m[4] || "";
        this.hash = m[5] || "";
        this.searchParams = new g.URLSearchParams(this.search);
        this.origin = this.protocol + "//" + this.host;
      }
      get href() {
        const q = this.searchParams.toString();
        return this.origin + this.pathname + (q ? "?" + q : "") + this.hash;
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
        this.url = typeof input === "string" ? input : input.url;
        this.method = String(init.method || (input && input.method) || "GET").toUpperCase();
        this.headers = new g.Headers(init.headers || (input && input.headers));
        this._body = init.body != null ? init.body : (input && input._body) || null;
      }
      clone() { return new g.Request(this.url, { method: this.method, headers: this.headers, body: this._body }); }
    };
    Object.assign(g.Request.prototype, body);
  }

  if (typeof g.Response === "undefined") {
    g.Response = class Response {
      constructor(b, init) {
        init = init || {};
        this._body = b != null ? b : null;
        this.status = init.status || 200;
        this.statusText = init.statusText || "";
        this.headers = new g.Headers(init.headers);
        this.ok = this.status >= 200 && this.status < 300;
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
})(globalThis);
