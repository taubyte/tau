// StarlingMonkey (SpiderMonkey) fetch-event bridge for the wasi:http/proxy
// world. Unlike the Javy tier, StarlingMonkey provides Web APIs natively
// (URL/Request/Response/Headers/fetch/streams/SubtleCrypto), so no polyfill is
// installed; this only adapts a Web-standard fetch handler (a function, or an
// object's `fetch`/`default`) to the incoming `fetch` event.
//
// env follows the Workers calling convention fetch(request, env, ctx); env.ASSETS
// 404s because Taubyte's static layer serves assets before the component.
export function serveComponent(app) {
  const fetchFn = typeof app === "function" ? app : app && (app.fetch || app.default);
  addEventListener("fetch", (event) => {
    if (typeof fetchFn !== "function") {
      event.respondWith(new Response("adapter: app has no fetch handler", { status: 500 }));
      return;
    }
    const env = (globalThis.__TAUBYTE_ENV__ = globalThis.__TAUBYTE_ENV__ || {});
    if (!env.ASSETS) {
      env.ASSETS = { fetch: () => Promise.resolve(new Response("Not Found", { status: 404 })) };
    }
    const ctx = { waitUntil() {}, passThroughOnException() {} };
    event.respondWith(
      Promise.resolve(fetchFn(event.request, env, ctx)).catch(
        (e) => new Response("adapter: " + (e && e.message ? e.message : String(e)), { status: 500 })
      )
    );
  });
}
