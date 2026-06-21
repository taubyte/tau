// StarlingMonkey (SpiderMonkey) fetch-event bridge for the wasi:http/proxy
// world. Unlike the Javy tier, StarlingMonkey provides Web APIs natively
// (URL/Request/Response/Headers/fetch/streams/SubtleCrypto), so no polyfill is
// installed; this only adapts a Web-standard fetch handler (a function, or an
// object's `fetch`/`default`) to the incoming `fetch` event and assembles the
// Workers-style `env`.
//
// env (the 2nd fetch arg, also what `cloudflare:workers` exports) is built from
// internal headers the substrate injects on the loopback request:
//   x-taubyte-env       JSON of secrets/config -> spread onto env (env.MY_SECRET)
//   x-taubyte-bindings  JSON {base, kv:[names], storage:[names]} -> each named
//                       binding becomes a fetch client: env.<name>.get/put/...
//                       (kv) or env.<name>.get/put (storage), against base.
// These headers are stripped before the app sees the request. env.ASSETS 404s
// because Taubyte's static layer serves assets before the component.
// Stream polyfills for the StarlingMonkey build jco currently ships (its
// ReadableStream is missing two things Next.js / React SSR rely on). Both are
// guarded so they no-op on an engine that already implements them.

// 1. Async iteration: `for await (const chunk of readableStream)`. Next.js SSR
//    drains the render stream this way; without Symbol.asyncIterator the engine
//    reports the stream as "not iterable".
(function () {
  if (typeof ReadableStream === "undefined" || ReadableStream.prototype[Symbol.asyncIterator]) return;
  const iter = function () {
    const reader = this.getReader();
    return {
      next() { return reader.read(); },
      return(v) {
        try { reader.releaseLock(); } catch (e) {}
        return Promise.resolve({ done: true, value: v });
      },
      [Symbol.asyncIterator]() { return this; },
    };
  };
  ReadableStream.prototype[Symbol.asyncIterator] = iter;
  if (!ReadableStream.prototype.values) ReadableStream.prototype.values = iter;
})();

// 2. tee() for byte streams (unimplemented in this build) — React's SSR stream
//    is a byte stream and the edge runtime tees it. Buffer the source once and
//    replay it into both branches (correct, though not zero-copy). Only used
//    when the native tee throws.
(function () {
  if (typeof ReadableStream === "undefined" || !ReadableStream.prototype.tee) return;
  const native = ReadableStream.prototype.tee;
  ReadableStream.prototype.tee = function () {
    try {
      return native.call(this);
    } catch (e) {
      return bufferingTee(this);
    }
  };
  function bufferingTee(stream) {
    const reader = stream.getReader();
    const chunks = [];
    let err = null;
    let draining = null;
    const drain = () => {
      if (!draining) {
        draining = (async () => {
          try {
            for (;;) {
              const { done, value } = await reader.read();
              if (done) break;
              chunks.push(value);
            }
          } catch (e) {
            err = e;
          }
        })();
      }
      return draining;
    };
    const branch = () => {
      let i = 0;
      return new ReadableStream({
        async pull(c) {
          await drain();
          if (err) return c.error(err);
          if (i < chunks.length) c.enqueue(chunks[i++]);
          else c.close();
        },
      });
    };
    return [branch(), branch()];
  }
})();

// 3. Request clone-with-body. `new Request(reqWithBody, { headers })` (the
//    next-on-pages worker re-wraps every request this way) traps on this build
//    (IndirectCallToNull) because the native clone-with-body path is a null stub
//    — but `new Request(url, init)` with the body passed in `init` works. Wrap
//    the constructor to take that path when input is a body-carrying Request and
//    init overrides fields. No-op-equivalent for the common cases.
(function (g) {
  if (typeof g.Request === "undefined") return;
  const Native = g.Request;
  function Request(input, init) {
    if (
      init &&
      typeof input !== "string" &&
      !(typeof URL !== "undefined" && input instanceof URL) &&
      input instanceof Native
    ) {
      const noBody = input.method === "GET" || input.method === "HEAD";
      const merged = Object.assign(
        { method: input.method, headers: input.headers, body: noBody ? undefined : input.body },
        init
      );
      return new Native(input.url, merged);
    }
    return init === undefined ? new Native(input) : new Native(input, init);
  }
  Request.prototype = Native.prototype;
  g.Request = Request;
})(globalThis);

export function serveComponent(app) {
  const fetchFn = typeof app === "function" ? app : app && (app.fetch || app.default);
  addEventListener("fetch", (event) => {
    globalThis.__TAUBYTE_SERVING = true; // request phase (gates init-time crypto fallback in node.js)
    if (typeof fetchFn !== "function") {
      event.respondWith(new Response("adapter: app has no fetch handler", { status: 500 }));
      return;
    }
    const request = event.request;
    const envHeader = request.headers.get("x-taubyte-env");
    const bindingsHeader = request.headers.get("x-taubyte-bindings");

    // Strip the internal headers so the app never sees them. The incoming
    // request's headers are immutable, so rebuild the request with a fresh
    // Headers (only when something needs stripping — leave plain requests as-is).
    let appReq = request;
    if (envHeader != null || bindingsHeader != null) {
      const h = new Headers(request.headers);
      h.delete("x-taubyte-env");
      h.delete("x-taubyte-bindings");
      try {
        appReq = new Request(request, { headers: h });
      } catch (e) {
        appReq = request;
      }
    }

    const env = (globalThis.__TAUBYTE_ENV__ = globalThis.__TAUBYTE_ENV__ || {});
    if (envHeader) {
      try {
        Object.assign(env, JSON.parse(envHeader));
      } catch (e) {}
    }
    if (!env.ASSETS) {
      env.ASSETS = { fetch: () => Promise.resolve(new Response("Not Found", { status: 404 })) };
    }
    if (bindingsHeader) {
      let cfg = null;
      try {
        cfg = JSON.parse(bindingsHeader);
      } catch (e) {}
      if (cfg && cfg.base) {
        const base = String(cfg.base).replace(/\/$/, "");
        for (const name of cfg.kv || []) {
          if (!env[name]) env[name] = makeKV(base + "/kv/" + name);
        }
        for (const name of cfg.storage || []) {
          if (!env[name]) env[name] = makeStorage(base + "/storage/" + name);
        }
      }
    }

    const ctx = { waitUntil() {}, passThroughOnException() {} };
    event.respondWith(
      Promise.resolve(fetchFn(appReq, env, ctx)).catch(
        (e) => new Response("adapter: " + (e && e.message ? e.message : String(e)), { status: 500 })
      )
    );
  });
}

// makeKV is a fetch client over the substrate KV binding endpoint:
//   GET    /kv/<key>            -> 200 value | 404 (miss)
//   PUT    /kv/<key>  body=val  -> 204
//   DELETE /kv/<key>            -> 204
//   GET    /kv?prefix=<p>       -> 200 ["key", ...]
function makeKV(base) {
  return {
    async get(key) {
      const r = await fetch(base + "/" + encodeURIComponent(key));
      if (r.status === 404) return null;
      if (!r.ok) throw new Error("KV get failed: " + r.status);
      return await r.text();
    },
    async put(key, value) {
      const r = await fetch(base + "/" + encodeURIComponent(key), { method: "PUT", body: String(value) });
      if (!r.ok) throw new Error("KV put failed: " + r.status);
    },
    async delete(key) {
      const r = await fetch(base + "/" + encodeURIComponent(key), { method: "DELETE" });
      if (!r.ok && r.status !== 404) throw new Error("KV delete failed: " + r.status);
    },
    async list(prefix) {
      const r = await fetch(base + "?prefix=" + encodeURIComponent(prefix || ""));
      return r.ok ? await r.json() : [];
    },
  };
}

// makeStorage is a fetch client over the substrate storage binding endpoint:
//   GET /storage/<path>           -> the file Response (or 404)
//   PUT /storage/<path> body=...  -> 204
function makeStorage(base) {
  return {
    async get(path) {
      const r = await fetch(base + "/" + String(path).replace(/^\//, ""));
      return r.ok ? r : null;
    },
    async put(path, body) {
      const r = await fetch(base + "/" + String(path).replace(/^\//, ""), { method: "PUT", body });
      if (!r.ok) throw new Error("storage put failed: " + r.status);
    },
  };
}
