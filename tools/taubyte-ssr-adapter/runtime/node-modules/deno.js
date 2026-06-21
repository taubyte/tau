// Deno runtime shim for `--mode deno`. Deno apps serve HTTP with
// `Deno.serve(handler)` / `Deno.serve(options, handler)` where the handler is a
// Web-standard `(Request) -> Response` — exactly what the StarlingMonkey
// component tier runs. This installs a global `Deno` whose serve() captures the
// handler instead of binding a socket; `serveDeno()` drives each wasi:http
// request through it.
//
// HTTP request-handling compatibility, not the full Deno runtime: no Deno.readFile
// (no filesystem), no Deno.connect (no raw sockets). Config/secrets come through
// Deno.env (backed by process.env, which the substrate populates from
// x-taubyte-env). Data goes through Taubyte primitives.

let _handler = null;

// Deno.serve forms: serve(handler), serve(options, handler),
// serve({ handler }), serve({ fetch }) (the `deno serve` default-export shape).
export function serve(arg1, arg2) {
  let handler = null;
  let options = {};
  if (typeof arg1 === "function") {
    handler = arg1;
  } else if (arg1 && typeof arg1 === "object") {
    options = arg1;
    if (typeof arg2 === "function") handler = arg2;
    else if (typeof arg1.handler === "function") handler = arg1.handler;
    else if (typeof arg1.fetch === "function") handler = arg1.fetch;
  }
  if (handler) _handler = handler;
  const port = options.port || 8000;
  const hostname = options.hostname || "0.0.0.0";
  if (typeof options.onListen === "function") {
    try { options.onListen({ hostname, port }); } catch (e) {}
  }
  return {
    finished: Promise.resolve(),
    addr: { transport: "tcp", hostname, port },
    shutdown() { return Promise.resolve(); },
    ref() {}, unref() {},
  };
}

// Deno.env over process.env (populated by the substrate from x-taubyte-env).
export const env = {
  get(k) { return (typeof process !== "undefined" && process.env) ? process.env[k] : undefined; },
  set(k, v) { if (typeof process !== "undefined" && process.env) process.env[k] = String(v); },
  has(k) { return !!(typeof process !== "undefined" && process.env && k in process.env); },
  delete(k) { if (typeof process !== "undefined" && process.env) delete process.env[k]; },
  toObject() { return Object.assign({}, typeof process !== "undefined" ? process.env : {}); },
};

function unavailable(name) {
  return () => { throw new Error("Deno." + name + " is not available in the Taubyte sandbox (no filesystem/sockets; use storage bindings)"); };
}

const Deno = {
  serve, env,
  pid: 1,
  noColor: true,
  args: [],
  build: { target: "wasm32-wasi", arch: "wasm32", os: "linux", vendor: "taubyte" },
  version: { deno: "1.x (taubyte-compat)", v8: "0", typescript: "0" },
  cwd() { return "/"; },
  exit() {},
  readTextFile: unavailable("readTextFile"),
  readFile: unavailable("readFile"),
  writeTextFile: unavailable("writeTextFile"),
  open: unavailable("open"),
  connect: unavailable("connect"),
  errors: { NotFound: class NotFound extends Error {} },
};

if (typeof globalThis.Deno === "undefined") globalThis.Deno = Deno;

export default Deno;

// serveDeno wires the captured Deno.serve handler to the wasi:http `fetch` event.
// Secrets injected via x-taubyte-env are merged into process.env (so Deno.env
// sees them); the internal loopback headers are stripped before the app runs.
export function serveDeno() {
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
      try { appReq = new Request(request, { headers: h }); } catch (e) { appReq = request; }
    }

    const info = { remoteAddr: { transport: "tcp", hostname: "127.0.0.1", port: 0 }, completed: Promise.resolve() };
    event.respondWith(
      Promise.resolve()
        .then(() => {
          if (typeof _handler !== "function") {
            return new Response("Deno: no handler registered (did the app call Deno.serve?)", { status: 500 });
          }
          return _handler(appReq, info);
        })
        .catch((e) => new Response("deno adapter: " + (e && e.message ? e.message : String(e)), { status: 500 }))
    );
  });
}
