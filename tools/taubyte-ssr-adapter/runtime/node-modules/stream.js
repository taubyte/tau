// node:stream shim — a functional subset (Readable/Writable/Duplex/Transform/
// PassThrough) on top of the EventEmitter shim. No real backpressure or
// highWaterMark accounting; chunks flow eagerly via microtasks. Enough for the
// stream usage in Express's dependency tree (send/finalhandler/iconv-lite) and
// for adapting request/response bodies.
//
// Authored as CommonJS so `require("stream")` returns the Stream class with
// .Readable/.Writable/... attached — the shape Node exposes and that callers
// like `send` rely on (they do `util.inherits(SendStream, require("stream"))`).

const EventEmitter = require("events");

function nextTick(fn, ...args) {
  (typeof queueMicrotask !== "undefined" ? queueMicrotask : (cb) => Promise.resolve().then(cb))(() => fn(...args));
}

class Stream extends EventEmitter {
  pipe(dest) {
    const src = this;
    src.on("data", (chunk) => dest.write(chunk));
    src.on("end", () => dest.end && dest.end());
    src.on("error", (e) => dest.emit && dest.emit("error", e));
    if (dest.emit) dest.emit("pipe", src);
    return dest;
  }
}

class Readable extends Stream {
  constructor(opts = {}) {
    super();
    this._readableState = { flowing: false, ended: false, buffer: [] };
    this.readable = true;
    this.readableEnded = false;
    this._encoding = opts.encoding || null;
    this.objectMode = !!opts.objectMode;
    if (typeof opts.read === "function") this._read = opts.read;
  }
  _read() {}
  setEncoding(enc) { this._encoding = enc; return this; }
  push(chunk) {
    const st = this._readableState;
    if (chunk === null) {
      st.ended = true;
      nextTick(() => this._drain());
      return false;
    }
    if (this._encoding && chunk instanceof Uint8Array) chunk = new TextDecoder(this._encoding).decode(chunk);
    st.buffer.push(chunk);
    if (st.flowing) nextTick(() => this._drain());
    else this.emit("readable");
    return true;
  }
  _drain() {
    const st = this._readableState;
    if (st.flowing) {
      while (st.buffer.length) this.emit("data", st.buffer.shift());
    }
    if (st.ended && st.buffer.length === 0 && !this.readableEnded) {
      this.readableEnded = true;
      this.readable = false;
      this.emit("end");
      this.emit("close");
    }
  }
  read() {
    const st = this._readableState;
    if (st.buffer.length) return st.buffer.shift();
    if (st.ended) return null;
    this._read();
    return st.buffer.length ? st.buffer.shift() : null;
  }
  on(ev, cb) {
    super.on(ev, cb);
    if (ev === "data") this.resume();
    return this;
  }
  resume() {
    if (!this._readableState.flowing) {
      this._readableState.flowing = true;
      nextTick(() => { this._read(); this._drain(); });
    }
    return this;
  }
  pause() { this._readableState.flowing = false; return this; }
  destroy(err) {
    this.readable = false;
    if (err) nextTick(() => this.emit("error", err));
    nextTick(() => this.emit("close"));
    return this;
  }
  [Symbol.asyncIterator]() {
    const st = this._readableState;
    const self = this;
    this.resume();
    return {
      next() {
        if (st.buffer.length) return Promise.resolve({ done: false, value: st.buffer.shift() });
        if (st.ended) return Promise.resolve({ done: true, value: undefined });
        return new Promise((resolve) => {
          const onData = (c) => { cleanup(); resolve({ done: false, value: c }); };
          const onEnd = () => { cleanup(); resolve({ done: true, value: undefined }); };
          const cleanup = () => { self.removeListener("data", onData); self.removeListener("end", onEnd); };
          self.once("data", onData);
          self.once("end", onEnd);
        });
      },
      return() { return Promise.resolve({ done: true, value: undefined }); },
    };
  }
  static from(iterable, opts) {
    const r = new Readable(opts);
    r._read = () => {};
    (async () => {
      try {
        for await (const chunk of iterable) r.push(chunk);
        r.push(null);
      } catch (e) { r.destroy(e); }
    })();
    return r;
  }
}

class Writable extends Stream {
  constructor(opts = {}) {
    super();
    this.writable = true;
    this.writableEnded = false;
    this.writableFinished = false;
    if (typeof opts.write === "function") this._write = opts.write;
    if (typeof opts.final === "function") this._final = opts.final;
  }
  _write(chunk, enc, cb) { cb && cb(); }
  write(chunk, enc, cb) {
    if (typeof enc === "function") { cb = enc; enc = null; }
    try {
      this._write(chunk, enc, (err) => { if (cb) cb(err); if (err) this.emit("error", err); });
    } catch (e) {
      if (cb) cb(e);
      this.emit("error", e);
      return false;
    }
    return true;
  }
  end(chunk, enc, cb) {
    if (typeof chunk === "function") { cb = chunk; chunk = null; }
    else if (typeof enc === "function") { cb = enc; enc = null; }
    const finish = () => {
      this.writableEnded = true;
      const done = () => {
        this.writableFinished = true;
        if (cb) cb();
        this.emit("finish");
        this.emit("close");
      };
      if (typeof this._final === "function") this._final(done);
      else done();
    };
    if (chunk != null) this.write(chunk, enc, finish);
    else nextTick(finish);
    return this;
  }
  cork() {}
  uncork() {}
  destroy(err) {
    this.writable = false;
    if (err) nextTick(() => this.emit("error", err));
    nextTick(() => this.emit("close"));
    return this;
  }
}

// Duplex: a Readable that also has the Writable surface. JS single-inheritance,
// so mix the Writable methods onto the prototype.
class Duplex extends Readable {
  constructor(opts = {}) {
    super(opts);
    this.writable = true;
    this.writableEnded = false;
    this.writableFinished = false;
    if (typeof opts.write === "function") this._write = opts.write;
    if (typeof opts.final === "function") this._final = opts.final;
  }
}
for (const m of ["_write", "write", "end", "cork", "uncork"]) {
  Duplex.prototype[m] = Writable.prototype[m];
}

class Transform extends Duplex {
  constructor(opts = {}) {
    super(opts);
    if (typeof opts.transform === "function") this._transform = opts.transform;
    if (typeof opts.flush === "function") this._flush = opts.flush;
  }
  _transform(chunk, enc, cb) { cb(null, chunk); }
  write(chunk, enc, cb) {
    if (typeof enc === "function") { cb = enc; enc = null; }
    try {
      this._transform(chunk, enc, (err, data) => {
        if (err) { this.emit("error", err); if (cb) cb(err); return; }
        if (data != null) this.push(data);
        if (cb) cb();
      });
    } catch (e) { this.emit("error", e); if (cb) cb(e); return false; }
    return true;
  }
  end(chunk, enc, cb) {
    if (typeof chunk === "function") { cb = chunk; chunk = null; }
    else if (typeof enc === "function") { cb = enc; enc = null; }
    const finishUp = () => {
      const done = () => { this.push(null); if (cb) cb(); this.emit("finish"); };
      if (typeof this._flush === "function") this._flush((err, data) => { if (data != null) this.push(data); done(); });
      else done();
    };
    if (chunk != null) this.write(chunk, enc, finishUp);
    else nextTick(finishUp);
    return this;
  }
}

class PassThrough extends Transform {
  _transform(chunk, enc, cb) { cb(null, chunk); }
}

function pipeline(...args) {
  const cb = typeof args[args.length - 1] === "function" ? args.pop() : null;
  const streams = args.flat();
  for (let i = 0; i < streams.length - 1; i++) streams[i].pipe(streams[i + 1]);
  const last = streams[streams.length - 1];
  if (cb) {
    last.on("finish", () => cb(null));
    last.on("end", () => cb(null));
    for (const s of streams) s.on && s.on("error", (e) => cb(e));
  }
  return last;
}

function finished(stream, cb) {
  let done = false;
  const fin = (err) => { if (!done) { done = true; cb(err || null); } };
  stream.on("end", () => fin());
  stream.on("finish", () => fin());
  stream.on("close", () => fin());
  stream.on("error", (e) => fin(e));
  return () => { done = true; };
}

Stream.Readable = Readable;
Stream.Writable = Writable;
Stream.Duplex = Duplex;
Stream.Transform = Transform;
Stream.PassThrough = PassThrough;
Stream.pipeline = pipeline;
Stream.finished = finished;
Stream.Stream = Stream;
Stream.default = Stream;

module.exports = Stream;
