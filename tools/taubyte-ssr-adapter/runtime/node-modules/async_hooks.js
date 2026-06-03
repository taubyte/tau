// node:async_hooks shim for Javy. Next/SvelteKit use AsyncLocalStorage for
// per-request context. The implementation lives on the global (see node.js) so
// it exists at module-evaluation time for runtimes that capture it eagerly; this
// shim re-exports the same classes so `import { AsyncLocalStorage } from
// "node:async_hooks"` and the global are identical. PROTOTYPE: single-flow only
// — no async-context tracking; run(store, cb) keeps the store set for the rest
// of the request (one request per module instance). Nested runs are last-wins.

export const AsyncLocalStorage = globalThis.AsyncLocalStorage;
export const AsyncResource = globalThis.AsyncResource;

export default { AsyncLocalStorage, AsyncResource };
