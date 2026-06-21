// Taubyte SSR bridge shim for Javy (QuickJS).
//
// Bare Javy provides ES2023 + console + TextEncoder/TextDecoder + Javy.IO, but
// NOT Web APIs (no fetch/Request/Response/URL/Headers). So the handler contract
// is plain JSON objects exchanged over WASI stdin/stdout:
//
//   handler(req) -> res         (sync or returning a Promise)
//     req : { method, url, headers: { [k]: v }, body: string }
//     res : { status, headers: { [k]: v }, body: string }
//
// This matches the envelope the substrate wasi-stdio path uses, so anything the
// substrate proved with the Go stand-in also runs here.
//
// Frameworks that need Web Request/Response (Hono, Next, ...) must bundle a
// polyfill that provides them and adapt their fetch handler to this contract;
// see README.md.

const STDIN = 0;
const STDOUT = 1;

function readAllStdin() {
  const chunks = [];
  const buf = new Uint8Array(4096);
  while (true) {
    const n = Javy.IO.readSync(STDIN, buf);
    if (n === 0) break;
    chunks.push(buf.slice(0, n));
  }
  let len = 0;
  for (const c of chunks) len += c.length;
  const all = new Uint8Array(len);
  let off = 0;
  for (const c of chunks) {
    all.set(c, off);
    off += c.length;
  }
  return new TextDecoder().decode(all);
}

function writeStdout(text) {
  const bytes = new TextEncoder().encode(text);
  let off = 0;
  while (off < bytes.length) {
    off += Javy.IO.writeSync(STDOUT, bytes.subarray(off));
  }
}

function envelope(res) {
  res = res || {};
  return JSON.stringify({
    status: res.status || 200,
    headers: res.headers || {},
    body: res.body == null ? "" : String(res.body),
  });
}

// serve wires a JSON request/response handler to the stdio ABI. The handler may
// be a function, a module namespace with a default export, or an object with a
// `handle`/`fetch` method.
export function serve(handler) {
  const fn =
    typeof handler === "function"
      ? handler
      : handler && (handler.default || handler.handle || handler.fetch);

  if (typeof fn !== "function") {
    writeStdout(envelope({ status: 500, body: "adapter: no handler exported" }));
    return;
  }

  let req;
  try {
    req = JSON.parse(readAllStdin() || "{}");
  } catch (e) {
    writeStdout(envelope({ status: 400, body: "adapter: bad request payload" }));
    return;
  }

  const fail = (e) =>
    writeStdout(envelope({ status: 500, body: "adapter: " + (e && e.message ? e.message : String(e)) }));

  let result;
  try {
    result = fn(req);
  } catch (e) {
    fail(e);
    return;
  }

  // Write synchronously for sync handlers so the response is flushed before the
  // module exits; only defer to a microtask when the handler is actually async
  // (which relies on Javy draining the job queue before _start returns).
  if (result && typeof result.then === "function") {
    result.then((res) => writeStdout(envelope(res))).catch(fail);
  } else {
    writeStdout(envelope(result));
  }
}

// serveFetch wires a Web-standard fetch handler (app.fetch(Request) -> Response,
// e.g. a Hono app) to the stdio ABI. Requires the Web API polyfill (web.js) to
// have installed Request/Response/Headers/URL on globalThis.
export function serveFetch(app) {
  const fetchFn = typeof app === "function" ? app : app && app.fetch;
  if (typeof fetchFn !== "function") {
    writeStdout(envelope({ status: 500, body: "adapter: app has no fetch handler" }));
    return;
  }

  const fail = (e) =>
    writeStdout(envelope({ status: 500, body: "adapter: " + (e && e.message ? e.message : String(e)) }));

  let payload;
  try {
    payload = JSON.parse(readAllStdin() || "{}");
  } catch (e) {
    writeStdout(envelope({ status: 400, body: "adapter: bad request payload" }));
    return;
  }

  // Reconstruct an absolute URL whose origin matches what the browser sent, so
  // CSRF checks (SvelteKit rejects form POSTs when request.url.origin !== the
  // Origin header) pass. The substrate forwards Host + X-Forwarded-Proto; fall
  // back to localhost only when running the bundle standalone.
  const hdr = (name) => {
    const h = payload.headers || {};
    name = name.toLowerCase();
    for (const k in h) if (k.toLowerCase() === name) return h[k];
    return undefined;
  };
  const u = payload.url || "/";
  let url;
  if (u.indexOf("http") === 0) {
    url = u;
  } else {
    const host = hdr("x-forwarded-host") || hdr("host") || "localhost";
    const proto = (hdr("x-forwarded-proto") || "http").split(",")[0].trim();
    url = proto + "://" + host + (u[0] === "/" ? u : "/" + u);
  }
  const method = (payload.method || "GET").toUpperCase();
  const req = new Request(url, {
    method,
    headers: payload.headers || {},
    body: method === "GET" || method === "HEAD" ? undefined : payload.body,
  });

  // Workers calling convention: fetch(request, env, ctx). env is the shared
  // bindings object (cloudflare:workers exports the same one). env.ASSETS
  // resolves against assets the adapter embedded (globalThis.__TAUBYTE_ASSETS__,
  // keyed by site-root path) — so a standalone bundle serves its own prerendered
  // pages and SvelteKit's read() gets real bytes; unknown paths 404 (the
  // substrate's static layer serves anything not embedded). Handlers that take
  // only the request (Hono) ignore the extra args.
  const env = (globalThis.__TAUBYTE_ENV__ = globalThis.__TAUBYTE_ENV__ || {});
  if (!env.ASSETS) {
    env.ASSETS = {
      fetch: function (input) {
        const store = globalThis.__TAUBYTE_ASSETS__ || {};
        let p;
        try {
          p = new URL(typeof input === "string" ? input : input.url).pathname;
        } catch (e) {
          p = typeof input === "string" ? input : (input && input.url) || "/";
        }
        const noSlash = p.replace(/\/$/, "");
        const hit =
          store[p] || store[noSlash] || store[p + "index.html"] || store[noSlash + "/index.html"];
        if (!hit) return Promise.resolve(new Response("Not Found", { status: 404 }));
        return Promise.resolve(new Response(hit.body, { status: 200, headers: { "content-type": hit.type } }));
      },
    };
  }
  const ctx = { waitUntil: function () {}, passThroughOnException: function () {} };

  Promise.resolve(fetchFn(req, env, ctx))
    .then(async (res) => {
      const headers = {};
      if (res && res.headers && typeof res.headers.forEach === "function") {
        res.headers.forEach((v, k) => (headers[k] = v));
      }
      const text =
        res && typeof res.text === "function"
          ? await res.text()
          : res && res._body != null
          ? String(res._body)
          : "";
      writeStdout(JSON.stringify({ status: (res && res.status) || 200, headers, body: text }));
    })
    .catch(fail);
}
