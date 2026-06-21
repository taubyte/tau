// node:http (+ node:https) compatibility for Taubyte's StarlingMonkey component
// tier — enough of the Node HTTP *server* surface to run frameworks built on
// `http.createServer((req, res) => ...)` / `app.listen()` (Express, Koa, Fastify,
// Connect, Nest's express adapter). There is no real socket: createServer just
// captures the request handler, and each incoming wasi:http request is adapted
// into a Node IncomingMessage / ServerResponse pair and driven through it.
//
// This is request-handler compatibility, not a Node runtime — no fs/net/
// child_process. Data goes through Taubyte bindings (env.KV / env.STORAGE).

let _handler = null;

// createServer(requestListener) — registers the handler; listen() is a no-op
// (there is no port to bind) that still invokes the ready callback.
export function createServer(opts, requestListener) {
  const h = typeof opts === "function" ? opts : requestListener;
  _handler = h;
  return new Server(h);
}

export class Server {
  constructor(h) {
    if (h) _handler = h;
    this._cbs = {};
    // Timeout/limit knobs frameworks (Fastify) read or assign; nominal here since
    // there is no real socket lifecycle.
    this.timeout = 0;
    this.keepAliveTimeout = 5000;
    this.headersTimeout = 60000;
    this.requestTimeout = 0;
    this.maxHeadersCount = null;
    this.maxConnections = Infinity;
    this.maxRequestsPerSocket = 0;
    this.listening = false;
  }
  listen(...args) {
    const cb = args.find((a) => typeof a === "function");
    this.listening = true;
    if (cb) queueMicrotask(cb);
    return this;
  }
  on(ev, cb) {
    if (ev === "request" && typeof cb === "function") _handler = cb;
    (this._cbs[ev] = this._cbs[ev] || []).push(cb);
    return this;
  }
  once(ev, cb) { return this.on(ev, cb); }
  emit() { return false; }
  removeListener() { return this; }
  off() { return this; }
  setTimeout(msecs, cb) { if (typeof msecs === "number") this.timeout = msecs; if (typeof msecs === "function") cb = msecs; if (typeof cb === "function") this.on("timeout", cb); return this; }
  address() { return { address: "127.0.0.1", port: 0, family: "IPv4" }; }
  ref() { return this; }
  unref() { return this; }
  closeAllConnections() {}
  closeIdleConnections() {}
  close(cb) { this.listening = false; if (cb) queueMicrotask(cb); return this; }
}

// IncomingMessage — a minimal readable request. method/url/headers are
// populated from the fetch Request; the body is delivered via 'data'/'end'
// events (what body parsers like express.json / raw-body consume) once a
// listener attaches.
export class IncomingMessage {
  constructor(method, url, headers, bodyBytes) {
    this.method = method;
    this.url = url; // path + query (Node servers see the path, not the origin)
    this.headers = headers;
    this.httpVersion = "1.1";
    this.httpVersionMajor = 1;
    this.httpVersionMinor = 1;
    this.complete = false;
    this.readable = true;
    // req.socket is an EventEmitter in Node, and libraries attach to it:
    // on-finished (Express's finalhandler 404/error path) does socket.on('close'/
    // 'error'), so it must have on/once or it throws and strands the response.
    // readable matters too: body-parser 2.x gates reading on isFinished(req),
    // which treats a non-readable socket as "body already done" and skips parsing.
    this.socket = {
      remoteAddress: "127.0.0.1", remotePort: 0, localAddress: "127.0.0.1", localPort: 0,
      readable: true, writable: true, encrypted: false, destroyed: false,
      on() { return this; }, once() { return this; }, removeListener() { return this; },
      off() { return this; }, emit() { return false; }, addListener() { return this; },
      setTimeout() { return this; }, setNoDelay() { return this; }, setKeepAlive() { return this; },
      destroy() { return this; }, ref() { return this; }, unref() { return this; },
    };
    this.connection = this.socket;
    this._rawBody = bodyBytes || new Uint8Array(0);
    this._listeners = {};
    this._flowed = false;
    this._encoding = null;
  }
  setEncoding(enc) { this._encoding = enc; return this; }
  on(ev, cb) {
    (this._listeners[ev] = this._listeners[ev] || []).push(cb);
    if (ev === "data" || ev === "end" || ev === "readable") this._flow();
    return this;
  }
  pipe(dest) {
    this.on("data", (c) => dest.write && dest.write(c));
    this.on("end", () => dest.end && dest.end());
    return dest;
  }
  once(ev, cb) {
    const wrap = (...a) => { this.removeListener(ev, wrap); cb(...a); };
    return this.on(ev, wrap);
  }
  removeListener(ev, cb) {
    if (this._listeners[ev]) this._listeners[ev] = this._listeners[ev].filter((f) => f !== cb);
    return this;
  }
  off(ev, cb) { return this.removeListener(ev, cb); }
  // Standard EventEmitter/stream surface some libraries reach for: finalhandler
  // (Express's 404/error path) calls unpipe(req) and on-finished inspects
  // listeners, so these must exist or it strands the response.
  listeners(ev) { return (this._listeners[ev] || []).slice(); }
  listenerCount(ev) { return (this._listeners[ev] || []).length; }
  removeAllListeners(ev) { if (ev) delete this._listeners[ev]; else this._listeners = {}; return this; }
  emit(ev, ...args) { for (const f of (this._listeners[ev] || []).slice()) f(...args); return (this._listeners[ev] || []).length > 0; }
  unpipe() { return this; }
  _emit(ev, arg) { for (const f of (this._listeners[ev] || []).slice()) f(arg); }
  _flow() {
    if (this._flowed) return;
    this._flowed = true;
    queueMicrotask(() => {
      try {
        if (this._rawBody && this._rawBody.length) {
          const chunk = this._encoding
            ? new TextDecoder(this._encoding).decode(this._rawBody)
            : (typeof Buffer !== "undefined" ? Buffer.from(this._rawBody) : this._rawBody);
          this._emit("data", chunk);
        }
        this.complete = true;
        this._emit("end");
      } catch (e) {
        // A throwing 'data' consumer (e.g. a body decoder) must not strand the
        // request: surface it as an 'error' so the reader's callback fires.
        this._emit("error", e);
      }
    });
  }
  // Async-iterable (for `for await (const c of req)`).
  [Symbol.asyncIterator]() {
    let sent = false;
    const self = this;
    return {
      next() {
        if (sent) return Promise.resolve({ done: true, value: undefined });
        sent = true;
        const v = self._encoding ? new TextDecoder(self._encoding).decode(self._rawBody)
          : (typeof Buffer !== "undefined" ? Buffer.from(self._rawBody) : self._rawBody);
        return Promise.resolve({ done: false, value: v });
      },
      return() { return Promise.resolve({ done: true, value: undefined }); },
    };
  }
  pause() { return this; }
  resume() { this._flow(); return this; }
  destroy() { return this; }
}

// ServerResponse — collects status/headers/body and resolves a promise the
// bridge awaits, which becomes the fetch Response.
export class ServerResponse {
  constructor(resolve) {
    this.statusCode = 200;
    this.statusMessage = "";
    this._headers = {};
    this._chunks = [];
    this._resolve = resolve;
    this._done = false;
    this.headersSent = false;
    this.finished = false;
    this._listeners = {};
  }
  setHeader(k, v) { this._headers[String(k).toLowerCase()] = v; return this; }
  getHeader(k) { return this._headers[String(k).toLowerCase()]; }
  getHeaders() { return Object.assign({}, this._headers); }
  hasHeader(k) { return String(k).toLowerCase() in this._headers; }
  removeHeader(k) { delete this._headers[String(k).toLowerCase()]; }
  writeHead(status, reasonOrHeaders, maybeHeaders) {
    this.statusCode = status;
    let headers = maybeHeaders;
    if (typeof reasonOrHeaders === "string") this.statusMessage = reasonOrHeaders;
    else headers = reasonOrHeaders;
    if (headers) for (const k in headers) this.setHeader(k, headers[k]);
    this.headersSent = true;
    return this;
  }
  write(chunk) {
    if (chunk != null) this._chunks.push(toBytes(chunk));
    return true;
  }
  end(chunk) {
    if (chunk != null) this._chunks.push(toBytes(chunk));
    this._finish();
  }
  _finish() {
    if (this._done) return;
    this._done = true;
    this.finished = true;
    let len = 0;
    for (const c of this._chunks) len += c.length;
    const body = new Uint8Array(len);
    let off = 0;
    for (const c of this._chunks) { body.set(c, off); off += c.length; }
    // Drop hop-by-hop / length headers the host sets itself.
    const headers = {};
    for (const k in this._headers) {
      if (k === "content-length" || k === "transfer-encoding" || k === "connection") continue;
      headers[k] = this._headers[k];
    }
    this._resolve(new Response(len ? body : null, { status: this.statusCode, headers }));
    this._emit("finish");
    this._emit("close");
  }
  on(ev, cb) { (this._listeners[ev] = this._listeners[ev] || []).push(cb); return this; }
  once(ev, cb) { return this.on(ev, cb); }
  removeListener(ev, cb) {
    if (this._listeners[ev]) this._listeners[ev] = this._listeners[ev].filter((f) => f !== cb);
    return this;
  }
  off(ev, cb) { return this.removeListener(ev, cb); }
  emit(ev, arg) { this._emit(ev, arg); return true; }
  _emit(ev, arg) { for (const f of this._listeners[ev] || []) f(arg); }
  flushHeaders() { this.headersSent = true; }
}

function toBytes(chunk) {
  if (chunk instanceof Uint8Array) return chunk;
  if (chunk instanceof ArrayBuffer) return new Uint8Array(chunk);
  return new TextEncoder().encode(String(chunk));
}

// __dispatch adapts a fetch Request to a Node req/res and runs the captured
// handler, resolving to the Response. Called by the node-server bridge.
export async function __dispatch(request) {
  if (typeof _handler !== "function") {
    return new Response("node: no request handler registered (did the app call listen()?)", { status: 500 });
  }
  const u = new URL(request.url);
  // Build the Node headers object, dropping the substrate's internal loopback
  // headers so the app never sees them.
  const headers = {};
  request.headers.forEach((v, k) => {
    if (k !== "x-taubyte-env" && k !== "x-taubyte-bindings") headers[k] = v;
  });
  let bodyBytes = new Uint8Array(0);
  if (request.method !== "GET" && request.method !== "HEAD") {
    bodyBytes = new Uint8Array(await request.arrayBuffer());
  }
  const req = new IncomingMessage(request.method, u.pathname + u.search, headers, bodyBytes);
  return new Promise((resolve, reject) => {
    const res = new ServerResponse(resolve);
    let ret;
    try {
      ret = _handler(req, res);
    } catch (e) {
      reject(e);
      return;
    }
    // Frameworks whose handler is async (Koa returns the middleware promise;
    // it ends the response only after it resolves) must not strand the request
    // on rejection — surface the error instead of hanging the event loop.
    if (ret && typeof ret.then === "function") {
      ret.then(undefined, (e) => { if (!res._done) reject(e); });
    }
  });
}

// serveNode wires the captured Node request handler to the wasi:http `fetch`
// event (the component tier's entrypoint). The app entry usually registers its
// handler as a side effect — `http.createServer(fn)` + `server.listen()`, or
// Express's `app.listen()` (which calls http.createServer(app) under the hood).
// `appDefault` is the entry's default/namespace export, adopted as the handler
// when the app default-exported it instead of calling listen() (serverless
// style). Secrets injected via x-taubyte-env are merged into process.env, the
// Node-idiomatic place an app reads configuration.
// Some frameworks boot asynchronously and can't finish that boot during the
// component's Wizer init snapshot (the pending job queue isn't preserved). Such a
// framework registers its boot (e.g. Fastify's app.ready) on
// globalThis.__TAUBYTE_DEFER_READY; we drive those once, on the first request, in
// the real event loop where the boot can complete. No-op when nothing registered.
let _deferredReady = null;
function _driveDeferredReady() {
  if (_deferredReady === null) {
    const fns = (typeof globalThis !== "undefined" && globalThis.__TAUBYTE_DEFER_READY) || [];
    _deferredReady = Promise.all(fns.map((f) => Promise.resolve().then(f)));
  }
  return _deferredReady;
}

export function serveNode(appDefault) {
  if (typeof _handler !== "function" && appDefault) {
    const cand = appDefault.default || appDefault;
    if (typeof cand === "function") _handler = cand;                       // (req,res)=>... or an Express/Connect app
    else if (cand && typeof cand.handle === "function") _handler = cand.handle.bind(cand);
    else if (cand && typeof cand.emit === "function" && cand._cbs) {        // a Server instance
      _handler = (req, res) => cand.emit("request", req, res);
    }
  }
  addEventListener("fetch", (event) => {
    globalThis.__TAUBYTE_SERVING = true; // request phase: secure WebCrypto is now usable
    const request = event.request;
    const envHeader = request.headers.get("x-taubyte-env");
    if (envHeader && typeof process !== "undefined" && process.env) {
      try { Object.assign(process.env, JSON.parse(envHeader)); } catch (e) {}
    }
    event.respondWith(
      _driveDeferredReady()
        .then(() => __dispatch(request))
        .catch((e) => new Response("node adapter: " + (e && e.message ? e.message : String(e)), { status: 500 }))
    );
  });
}

export const METHODS = ["GET", "HEAD", "POST", "PUT", "DELETE", "PATCH", "OPTIONS", "CONNECT", "TRACE"];
export const STATUS_CODES = { 200: "OK", 201: "Created", 204: "No Content", 301: "Moved Permanently", 302: "Found", 304: "Not Modified", 400: "Bad Request", 401: "Unauthorized", 403: "Forbidden", 404: "Not Found", 405: "Method Not Allowed", 500: "Internal Server Error" };

export default { createServer, Server, IncomingMessage, ServerResponse, METHODS, STATUS_CODES, __dispatch, serveNode };
