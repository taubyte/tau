// node:events shim — a minimal EventEmitter used widely across the npm
// ecosystem. Authored as CommonJS so `require("events")` returns the
// EventEmitter constructor itself (the shape Node exposes: callers do both
// `require("events")` as the class and `require("events").EventEmitter`).

class EventEmitter {
  constructor() {
    this._e = { __proto__: null };
  }
  // Lazily materialise the event map. Express (and other libs) copy these
  // prototype methods onto a plain object/function without ever calling the
  // constructor, so `this._e` may not exist yet — match Node's lazy init.
  _events() {
    if (!this._e) this._e = { __proto__: null };
    return this._e;
  }
  on(t, f) {
    const e = this._events();
    (e[t] = e[t] || []).push(f);
    return this;
  }
  addListener(t, f) {
    return this.on(t, f);
  }
  prependListener(t, f) {
    const e = this._events();
    (e[t] = e[t] || []).unshift(f);
    return this;
  }
  once(t, f) {
    const self = this;
    const g = function () {
      self.off(t, g);
      return f.apply(null, arguments);
    };
    g.listener = f;
    return this.on(t, g);
  }
  off(t, f) {
    const e = this._events();
    if (e[t]) e[t] = e[t].filter((x) => x !== f && x.listener !== f);
    return this;
  }
  removeListener(t, f) {
    return this.off(t, f);
  }
  removeAllListeners(t) {
    const e = this._events();
    if (t) delete e[t];
    else this._e = { __proto__: null };
    return this;
  }
  emit(t) {
    const e = this._events();
    const args = Array.prototype.slice.call(arguments, 1);
    const ls = (e[t] || []).slice();
    for (const f of ls) f.apply(this, args);
    return ls.length > 0;
  }
  listeners(t) {
    return (this._events()[t] || []).slice();
  }
  rawListeners(t) {
    return (this._events()[t] || []).slice();
  }
  listenerCount(t) {
    return (this._events()[t] || []).length;
  }
  eventNames() {
    return Object.keys(this._events());
  }
  setMaxListeners() {
    return this;
  }
  getMaxListeners() {
    return 10;
  }
}

EventEmitter.EventEmitter = EventEmitter;
EventEmitter.default = EventEmitter;
EventEmitter.defaultMaxListeners = 10;
EventEmitter.once = function (emitter, name) {
  return new Promise((resolve) => emitter.once(name, (...args) => resolve(args)));
};

module.exports = EventEmitter;
