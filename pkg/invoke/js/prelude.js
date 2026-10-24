// The platform prelude. warpjs gives a realm the ECMAScript builtins and
// nothing else; a Workers-shape handler also expects Request, Response,
// Headers, TextEncoder, TextDecoder and console. This runs once per realm,
// before the bundle, and evaluates to a function the Go side calls with its
// host functions. What that returns is the glue, which only Go holds.
(function (host) {
  "use strict";

  const define = (name, value) =>
    Object.defineProperty(globalThis, name, { value, writable: true, configurable: true, enumerable: false });

  // ── the window ────────────────────────────────────────────────────────

  // Bytes cross to Go and back through this one buffer, never as values, so
  // nothing a request carried stays pinned in the arena after it. It is
  // written and read with no other JS in between.
  let win = new ArrayBuffer(16 << 10);
  host.window(win);

  const reserve = (n) => {
    if (n > win.byteLength) {
      win = new ArrayBuffer(Math.max(n, win.byteLength * 2));
      host.window(win);
    }
  };

  // ArrayBuffer.prototype.slice copies as a block, and a typed array's set and
  // slice copy element by element, a hundred times slower. So bytes leave the
  // window by slicing its buffer, and enter it as 32-bit words: a Float64Array
  // would be eight bytes at a time and is not exact, since it rewrites NaNs.
  const copyOut = (n) => new Uint8Array(win.slice(0, n));

  const copyIn = (bytes) => {
    const n = bytes.length;
    reserve(n);
    let done = 0;
    if (bytes.byteOffset % 4 === 0) {
      done = n & ~3;
      new Int32Array(win, 0, done / 4).set(new Int32Array(bytes.buffer, bytes.byteOffset, done / 4));
    }
    new Uint8Array(win, done, n - done).set(bytes.subarray(done));
    return n;
  };

  // ── text ──────────────────────────────────────────────────────────────

  const encode = (s) => {
    s = String(s);
    let n = host.encode(s);
    if (n < 0) {
      reserve(-n);
      n = host.encode(s);
    }
    return copyOut(n);
  };

  const decode = (bytes, fatal, stream) => host.decode(copyIn(bytes), fatal, stream);

  const view = (input) => {
    if (input instanceof ArrayBuffer) return new Uint8Array(input);
    if (ArrayBuffer.isView(input)) return new Uint8Array(input.buffer, input.byteOffset, input.byteLength);
    throw new TypeError("The provided value is not of type '(ArrayBuffer or ArrayBufferView)'");
  };

  const LABELS = ["utf-8", "utf8", "unicode-1-1-utf-8", "unicode11utf8", "unicode20utf8", "x-unicode20utf8"];

  class TextEncoder {
    get encoding() { return "utf-8"; }

    encode(input = "") { return encode(input); }

    encodeInto(source, destination) {
      if (!(destination instanceof Uint8Array)) throw new TypeError("The provided value is not of type 'Uint8Array'");
      source = String(source);
      let read = 0;
      let written = 0;
      for (const ch of source) {
        const cp = ch.codePointAt(0);
        const n = cp < 0x80 ? 1 : cp < 0x800 ? 2 : cp < 0x10000 ? 3 : 4;
        if (written + n > destination.length) break;
        written += n;
        read += ch.length;
      }
      destination.set(encode(source.slice(0, read)));
      return { read, written };
    }
  }

  class TextDecoder {
    #fatal;
    #ignoreBOM;
    #pending = null;
    #bomSeen = false;

    constructor(label = "utf-8", options = undefined) {
      if (!LABELS.includes(String(label).trim().toLowerCase())) {
        throw new RangeError(`The "${label}" encoding is not supported`);
      }
      this.#fatal = Boolean(options?.fatal);
      this.#ignoreBOM = Boolean(options?.ignoreBOM);
    }

    get encoding() { return "utf-8"; }
    get fatal() { return this.#fatal; }
    get ignoreBOM() { return this.#ignoreBOM; }

    decode(input = undefined, options = undefined) {
      const stream = Boolean(options?.stream);
      let bytes = input === undefined ? new Uint8Array(0) : view(input);
      if (this.#pending !== null) {
        const joined = new Uint8Array(this.#pending.length + bytes.length);
        joined.set(this.#pending);
        joined.set(bytes, this.#pending.length);
        bytes = joined;
        this.#pending = null;
      }
      const out = decode(bytes, this.#fatal, stream);
      if (out === null) {
        this.#bomSeen = false;
        throw new TypeError("The encoded data was not valid for encoding utf-8");
      }
      let [text, rest] = out;
      if (rest > 0) this.#pending = bytes.slice(bytes.length - rest);
      if (!this.#ignoreBOM && !this.#bomSeen && text.length > 0) {
        this.#bomSeen = true;
        if (text.charCodeAt(0) === 0xfeff) text = text.slice(1);
      }
      if (!stream) {
        this.#bomSeen = false;
        this.#pending = null;
      }
      return text;
    }
  }

  // ── headers ───────────────────────────────────────────────────────────

  const TOKEN = /^[!#$%&'*+\-.^_`|~0-9A-Za-z]+$/;

  const headerName = (n) => {
    n = String(n);
    if (!TOKEN.test(n)) throw new TypeError(`Invalid header name: "${n}"`);
    return n.toLowerCase();
  };

  const headerValue = (v) => {
    v = String(v).replace(/^[\t\n\r ]+|[\t\n\r ]+$/g, "");
    if (/[\0\r\n]/.test(v)) throw new TypeError(`Invalid header value: "${v}"`);
    return v;
  };

  let pairsOf;

  class Headers {
    #list = [];

    static {
      pairsOf = (h) => h.#list;
    }

    constructor(init = undefined) {
      if (init === undefined || init === null) return;
      if (typeof init !== "object" && typeof init !== "function") {
        throw new TypeError("Headers takes a record or a sequence of pairs");
      }
      if (#list in init) {
        for (const [n, v] of init.#list) this.#list.push([n, v]);
        return;
      }
      if (typeof init[Symbol.iterator] === "function") {
        for (const pair of init) {
          const p = [...pair];
          if (p.length !== 2) throw new TypeError("A header pair must have exactly two items");
          this.append(p[0], p[1]);
        }
        return;
      }
      for (const k of Object.keys(init)) this.append(k, init[k]);
    }

    append(name, value) { this.#list.push([headerName(name), headerValue(value)]); }

    delete(name) {
      name = headerName(name);
      this.#list = this.#list.filter((p) => p[0] !== name);
    }

    get(name) {
      name = headerName(name);
      let out = null;
      for (const p of this.#list) if (p[0] === name) out = out === null ? p[1] : `${out}, ${p[1]}`;
      return out;
    }

    getSetCookie() { return this.#list.filter((p) => p[0] === "set-cookie").map((p) => p[1]); }

    has(name) {
      name = headerName(name);
      return this.#list.some((p) => p[0] === name);
    }

    set(name, value) {
      name = headerName(name);
      value = headerValue(value);
      let seen = false;
      this.#list = this.#list.flatMap((p) => (p[0] !== name ? [p] : seen ? [] : ((seen = true), [[name, value]])));
      if (!seen) this.#list.push([name, value]);
    }

    *entries() {
      const names = [...new Set(this.#list.map((p) => p[0]))].sort();
      for (const n of names) {
        if (n === "set-cookie") for (const v of this.getSetCookie()) yield [n, v];
        else yield [n, this.get(n)];
      }
    }

    *keys() { for (const [n] of this.entries()) yield n; }
    *values() { for (const [, v] of this.entries()) yield v; }

    forEach(callback, thisArg = undefined) {
      for (const [n, v] of this.entries()) callback.call(thisArg, v, n, this);
    }

    [Symbol.iterator]() { return this.entries(); }
    get [Symbol.toStringTag]() { return "Headers"; }
  }

  // ── bodies ────────────────────────────────────────────────────────────

  // A body is a string, a Uint8Array in a buffer of its own, or null. A string
  // stays one until something asks for bytes, so a text response reaches Go as
  // the string it was written as.
  const extract = (body, headers) => {
    if (body === null || body === undefined) return null;
    if (typeof body === "string") {
      if (!headers.has("content-type")) headers.set("content-type", "text/plain;charset=UTF-8");
      return body;
    }
    if (body instanceof ArrayBuffer) return new Uint8Array(body.slice(0));
    if (ArrayBuffer.isView(body)) {
      return new Uint8Array(body.buffer.slice(body.byteOffset, body.byteOffset + body.byteLength));
    }
    return extract(String(body), headers);
  };

  const copyBody = (body) => (body === null || typeof body === "string" ? body : new Uint8Array(body.buffer.slice(0)));

  const bytesOf = (body) => (typeof body === "string" ? encode(body) : body);

  const textOf = (body) => (typeof body === "string" ? body : new TextDecoder().decode(body));

  let bodyOf;

  class Body {
    #body;
    #used = false;

    static {
      bodyOf = (b) => {
        if (b.#used) throw new TypeError("Body has already been used");
        return b.#body;
      };
    }

    constructor(body) { this.#body = body; }

    get bodyUsed() { return this.#body !== null && this.#used; }

    #take() {
      if (this.#body === null) return "";
      if (this.#used) throw new TypeError("Body has already been used");
      this.#used = true;
      return this.#body;
    }

    async arrayBuffer() { return bytesOf(this.#take()).buffer; }
    async bytes() { return bytesOf(this.#take()); }
    async text() { return textOf(this.#take()); }
    async json() { return JSON.parse(textOf(this.#take())); }
  }

  // ── request ───────────────────────────────────────────────────────────

  const INCOMING = Symbol("incoming");
  const NORMALIZED = ["DELETE", "GET", "HEAD", "OPTIONS", "POST", "PUT"];
  const FORBIDDEN = ["CONNECT", "TRACE", "TRACK"];

  class Request extends Body {
    #method;
    #url;
    #headers;

    constructor(input, init = undefined) {
      if (input === INCOMING) {
        super(init.body);
        this.#method = init.method;
        this.#url = init.url;
        this.#headers = init.headers;
        return;
      }
      init = init ?? {};
      const from = input !== null && typeof input === "object" && #method in input ? input : null;
      let method = init.method !== undefined ? String(init.method) : from ? from.#method : "GET";
      if (!TOKEN.test(method)) throw new TypeError(`'${method}' is not a valid HTTP method`);
      const upper = method.toUpperCase();
      if (FORBIDDEN.includes(upper)) throw new TypeError(`'${method}' HTTP method is unsupported`);
      if (NORMALIZED.includes(upper)) method = upper;
      const headers = new Headers(init.headers !== undefined ? init.headers : from ? from.#headers : undefined);
      let bytes = null;
      if (init.body !== undefined && init.body !== null) bytes = extract(init.body, headers);
      else if (init.body === undefined && from) bytes = copyBody(bodyOf(from));
      if ((method === "GET" || method === "HEAD") && bytes !== null) {
        throw new TypeError("Request with GET/HEAD method cannot have body");
      }
      super(bytes);
      this.#method = method;
      this.#url = from ? from.#url : String(input);
      this.#headers = headers;
    }

    get method() { return this.#method; }
    get url() { return this.#url; }
    get headers() { return this.#headers; }
    get redirect() { return "follow"; }

    clone() { return new Request(this); }
    get [Symbol.toStringTag]() { return "Request"; }
  }

  // ── response ──────────────────────────────────────────────────────────

  const NULL_BODY = [101, 103, 204, 205, 304];
  const REDIRECT = [301, 302, 303, 307, 308];

  // respond hands an answer to Go: the status, the header pairs, and the body,
  // as the string it is or through the window (-1 for none).
  const respond = (status, flat, body) => {
    if (typeof body === "string") host.respondText(status, flat, body);
    else host.respond(status, flat, body === null ? -1 : copyIn(body));
  };

  let answerOf;

  class Response extends Body {
    #status;
    #statusText;
    #headers;

    static {
      answerOf = (r) => {
        if (r === null || typeof r !== "object" || !(#status in r)) {
          throw new TypeError("The handler did not answer with a Response");
        }
        respond(r.#status, pairsOf(r.#headers).flat(), bodyOf(r));
      };
    }

    constructor(body = null, init = undefined) {
      init = init ?? {};
      const status = init.status === undefined ? 200 : Number(init.status);
      if (!Number.isInteger(status) || status < 200 || status > 599) {
        throw new RangeError(`The status provided (${init.status}) is outside the range [200, 599]`);
      }
      const headers = new Headers(init.headers);
      let bytes = null;
      if (body !== null && body !== undefined) {
        if (NULL_BODY.includes(status)) throw new TypeError(`A response with status ${status} cannot have a body`);
        bytes = extract(body, headers);
      }
      super(bytes);
      this.#status = status;
      this.#statusText = init.statusText === undefined ? "" : String(init.statusText);
      this.#headers = headers;
    }

    static json(data, init = undefined) {
      const text = JSON.stringify(data);
      if (text === undefined) throw new TypeError("The value is not JSON serializable");
      const headers = new Headers(init?.headers);
      if (!headers.has("content-type")) headers.set("content-type", "application/json");
      return new Response(text, { status: init?.status, statusText: init?.statusText, headers });
    }

    static redirect(url, status = 302) {
      status = Number(status);
      if (!REDIRECT.includes(status)) throw new RangeError(`Invalid redirect status ${status}`);
      return new Response(null, { status, headers: { location: String(url) } });
    }

    get status() { return this.#status; }
    get ok() { return this.#status >= 200 && this.#status <= 299; }
    get statusText() { return this.#statusText; }
    get headers() { return this.#headers; }
    get type() { return "default"; }
    get url() { return ""; }
    get redirected() { return false; }

    clone() {
      return new Response(copyBody(bodyOf(this)), {
        status: this.#status,
        statusText: this.#statusText,
        headers: this.#headers,
      });
    }

    get [Symbol.toStringTag]() { return "Response"; }
  }

  // ── console ───────────────────────────────────────────────────────────

  const show = (v) => {
    if (typeof v === "string") return v;
    if (typeof v === "function") return `[Function: ${v.name || "(anonymous)"}]`;
    if (typeof v === "bigint") return `${v}n`;
    if (v === null || typeof v !== "object") return String(v);
    if (typeof v.stack === "string") return v.stack;
    try {
      return JSON.stringify(v, (k, x) => (typeof x === "bigint" ? `${x}n` : x)) ?? String(v);
    } catch {
      return String(v);
    }
  };

  const line = (args) => {
    try {
      return args.map(show).join(" ");
    } catch {
      return "[unprintable]";
    }
  };

  const print = (level) => (...args) => host.log(level, line(args));

  const console = {
    log: print(0),
    info: print(0),
    debug: print(0),
    trace: print(0),
    warn: print(1),
    error: print(1),
  };

  const report = (e) => host.log(1, `Uncaught ${line([e])}`);

  define("Headers", Headers);
  define("Request", Request);
  define("Response", Response);
  define("TextEncoder", TextEncoder);
  define("TextDecoder", TextDecoder);
  define("console", console);

  // ── glue ──────────────────────────────────────────────────────────────

  const failure = () => respond(500, ["content-type", "text/plain;charset=UTF-8"], "Internal Server Error");

  // drain settles what ctx.waitUntil was handed, including what is handed to
  // it while the earlier ones run, and then lets go of all of it.
  const drain = async (waits) => {
    for (let done = 0; done < waits.length; ) {
      const batch = waits.slice(done);
      done = waits.length;
      for (const r of await Promise.allSettled(batch)) if (r.status === "rejected") report(r.reason);
    }
    waits.length = 0;
  };

  // context is a request's ctx, and what settles the work handed to it. It
  // closes over nothing of the request's, since it outlives the request. Go
  // is told when there is work, and asks for settle only then.
  const context = () => {
    const waits = [];
    return {
      ctx: {
        waitUntil(p) {
          waits.push(Promise.resolve(p));
          host.waiting();
        },
        // There is no origin to pass a request through to, so an exception
        // is a 500 either way.
        passThroughOnException() {},
      },
      settle: () => (waits.length === 0 ? undefined : drain(waits)),
    };
  };

  // current is the request being served: its answer, and what settles its
  // waitUntil work. The answer reaches Go through respond, so Go asks for
  // these only when it has to wait: a value Go holds stays pinned.
  let current = null;

  const serveWith = (handler) => {
    const env = {};
    return () => {
      const [method, url, flat, n] = host.head();
      const headers = new Headers();
      const list = pairsOf(headers);
      for (let i = 0; i < flat.length; i += 2) list.push([flat[i], flat[i + 1]]);
      const body = n === 0 ? null : copyOut(n);
      const request = new Request(INCOMING, { method, url, headers, body });
      const { ctx, settle } = context();
      let answer;
      try {
        answer = Promise.resolve(handler.fetch(request, env, ctx));
      } catch (e) {
        answer = Promise.reject(e);
      }
      answer = answer.then(answerOf).catch((e) => {
        report(e);
        failure();
      });
      current = { answer, settle };
    };
  };

  return Object.freeze({
    // reserve grows the window to hold n bytes.
    reserve(n) { reserve(n); },

    // answer is the request's answer: settled once Go has its response.
    answer() { return current.answer; },

    // settle settles the request's waitUntil work, or is undefined when there
    // is none.
    settle() { return current.settle(); },

    // bind finds the handler an export is, or says why there is none.
    bind(ns, name) {
      const what = name === "default" ? "default export" : `export "${name}"`;
      if (!(name in ns)) return `the entry has no ${what}`;
      const handler = ns[name];
      if (handler === null || (typeof handler !== "object" && typeof handler !== "function")) {
        return `its ${what} is not an object`;
      }
      if (typeof handler.fetch !== "function") return `its ${what} has no fetch method`;
      return serveWith(handler);
    },

    // guard is p, raced against a cancellation the Go side can trigger.
    guard(p) {
      let cancel;
      const stopped = new Promise((_, reject) => {
        cancel = reject;
      });
      return [Promise.race([p, stopped]), () => cancel(new Error("cancelled"))];
    },
  });
});
