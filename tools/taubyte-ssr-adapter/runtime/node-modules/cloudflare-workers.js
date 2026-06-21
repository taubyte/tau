// cloudflare:workers shim — Cloudflare's Workers runtime virtual module, emitted
// by edge adapters (SvelteKit adapter-cloudflare, etc.). Taubyte is not
// Cloudflare, so there are no platform bindings. `env` is a shared object (the
// same one the shim's serveFetch passes as the fetch handler's 2nd arg), with an
// ASSETS stub that 404s — static assets are served by Taubyte's static layer
// before the request ever reaches the bundle. Wire real bindings to Taubyte
// KV/secrets/storage later.

const env = (globalThis.__TAUBYTE_ENV__ = globalThis.__TAUBYTE_ENV__ || {});

export { env };

export class WorkerEntrypoint {
  constructor(ctx, e) {
    this.ctx = ctx;
    this.env = e || env;
  }
}
export class DurableObject {
  constructor(ctx, e) {
    this.ctx = ctx;
    this.env = e || env;
  }
}
export class RpcStub {}
export class RpcTarget {}

export default { env, WorkerEntrypoint, DurableObject, RpcStub, RpcTarget };
