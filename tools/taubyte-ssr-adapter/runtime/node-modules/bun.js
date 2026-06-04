// Bun runtime shim for `--mode bun`. Bun apps serve HTTP with
// `Bun.serve({ fetch(request, server) -> Response })` — a Web-standard fetch
// handler, which is exactly what the StarlingMonkey component tier runs. This
// installs a global `Bun` (and is importable as `bun`) whose `serve()` captures
// the fetch handler instead of binding a socket; `serveBun()` then drives each
// wasi:http request through it.
//
// HTTP request-handling compatibility, not the full Bun runtime: no Bun.file
// (no filesystem), no WebSocket upgrade. Config/secrets come through
// process.env / Bun.env; data goes through Taubyte primitives.

let _fetch = null;
let _error = null;

// A Bun.Server-like handle. The port/hostname are nominal (no socket is bound);
// reload() swaps the handler, and fetch() lets code call the server directly.
function makeServer(options) {
  return {
    port: (options && options.port) || 3000,
    hostname: (options && options.hostname) || "localhost",
    development: false,
    url: new URL("http://localhost:" + ((options && options.port) || 3000) + "/"),
    pendingRequests: 0,
    stop() {},
    ref() {},
    unref() {},
    reload(o) {
      if (o && typeof o.fetch === "function") _fetch = o.fetch;
      if (o && typeof o.error === "function") _error = o.error;
    },
    upgrade() { return false; }, // no WebSocket upgrade on the component tier
    requestIP() { return { address: "127.0.0.1", family: "IPv4", port: 0 }; },
    fetch(req) { return _fetch ? _fetch(req, this) : new Response("Bun: no fetch handler", { status: 500 }); },
  };
}

export function serve(options) {
  options = options || {};
  if (typeof options.fetch === "function") _fetch = options.fetch;
  if (typeof options.error === "function") _error = options.error;
  return makeServer(options);
}

// Bun.file — no filesystem here; report absence rather than crash at import.
export function file(path) {
  const fail = () => Promise.reject(new Error("Bun.file is not available in the Taubyte sandbox (no filesystem; use storage bindings)"));
  return {
    name: String(path), size: 0, type: "",
    exists: () => Promise.resolve(false),
    text: fail, arrayBuffer: fail, json: fail, bytes: fail,
    stream() { throw new Error("Bun.file is not available in the Taubyte sandbox"); },
  };
}

export const env = typeof process !== "undefined" && process.env ? process.env : {};

export function sleep(ms) { return new Promise((r) => setTimeout(r, ms)); }
export function sleepSync() {}
export function nanoseconds() { return typeof performance !== "undefined" ? Math.floor(performance.now() * 1e6) : 0; }

const Bun = {
  serve, file, env, sleep, sleepSync, nanoseconds,
  version: "1.x (taubyte-compat)",
  revision: "taubyte",
  main: "",
};

// Apps use the global `Bun`; install it before the entry runs.
if (typeof globalThis.Bun === "undefined") globalThis.Bun = Bun;

export default Bun;

// serveBun wires the captured Bun.serve fetch handler to the wasi:http `fetch`
// event. Secrets injected via x-taubyte-env are merged into process.env (and
// thus Bun.env, which aliases it). The internal loopback headers are stripped
// before the app sees the request.
export function serveBun() {
  addEventListener("fetch", (event) => {
    globalThis.__TAUBYTE_SERVING = true; // request phase: secure WebCrypto is now usable
    const request = event.request;
    const envHeader = request.headers.get("x-taubyte-env");
    if (envHeader && typeof process !== "undefined" && process.env) {
      try { Object.assign(process.env, JSON.parse(envHeader)); } catch (e) {}
    }

    let appReq = request;
    if (request.headers.has("x-taubyte-env") || request.headers.has("x-taubyte-bindings")) {
      const h = new Headers(request.headers);
      h.delete("x-taubyte-env");
      h.delete("x-taubyte-bindings");
      // new Request(req, { headers }) is reconstructed via the component shim's
      // clone-with-body polyfill; fall back to the original on any failure.
      try { appReq = new Request(request, { headers: h }); } catch (e) { appReq = request; }
    }

    const server = makeServer({});
    event.respondWith(
      Promise.resolve()
        .then(() => {
          if (typeof _fetch !== "function") {
            return new Response("Bun: no fetch handler registered (did the app call Bun.serve?)", { status: 500 });
          }
          return _fetch(appReq, server);
        })
        .catch((e) => {
          if (typeof _error === "function") {
            try { return _error(e); } catch (_) {}
          }
          return new Response("bun adapter: " + (e && e.message ? e.message : String(e)), { status: 500 });
        })
    );
  });
}
