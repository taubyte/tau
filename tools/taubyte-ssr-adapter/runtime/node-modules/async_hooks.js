// node:async_hooks shim for Javy. Next/SvelteKit use AsyncLocalStorage for
// per-request context. PROTOTYPE: single-flow only — there is no async-context
// tracking, so `run(store, cb)` keeps the store set for the rest of the request
// (which suits one request per module instance). Nested runs are last-wins.

export class AsyncLocalStorage {
  run(store, cb, ...args) {
    this._store = store;
    return cb(...args);
  }
  getStore() {
    return this._store;
  }
  enterWith(store) {
    this._store = store;
  }
  exit(cb, ...args) {
    const prev = this._store;
    this._store = undefined;
    try {
      return cb(...args);
    } finally {
      this._store = prev;
    }
  }
  disable() {
    this._store = undefined;
  }
}

export class AsyncResource {
  constructor() {}
  runInAsyncScope(fn, thisArg, ...args) {
    return fn.apply(thisArg, args);
  }
  bind(fn) {
    return fn;
  }
}

export default { AsyncLocalStorage, AsyncResource };
