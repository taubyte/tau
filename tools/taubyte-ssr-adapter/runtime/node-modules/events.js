// node:events shim for Javy — a minimal EventEmitter used widely across the
// npm ecosystem.

export class EventEmitter {
  constructor() {
    this._e = {};
  }
  on(t, f) {
    (this._e[t] = this._e[t] || []).push(f);
    return this;
  }
  addListener(t, f) {
    return this.on(t, f);
  }
  once(t, f) {
    const self = this;
    const g = function () {
      self.off(t, g);
      return f.apply(null, arguments);
    };
    return this.on(t, g);
  }
  off(t, f) {
    if (this._e[t]) this._e[t] = this._e[t].filter((x) => x !== f);
    return this;
  }
  removeListener(t, f) {
    return this.off(t, f);
  }
  removeAllListeners(t) {
    if (t) delete this._e[t];
    else this._e = {};
    return this;
  }
  emit(t) {
    const args = Array.prototype.slice.call(arguments, 1);
    const ls = (this._e[t] || []).slice();
    for (const f of ls) f.apply(null, args);
    return ls.length > 0;
  }
  listeners(t) {
    return (this._e[t] || []).slice();
  }
  listenerCount(t) {
    return (this._e[t] || []).length;
  }
}

EventEmitter.EventEmitter = EventEmitter;
export default EventEmitter;
