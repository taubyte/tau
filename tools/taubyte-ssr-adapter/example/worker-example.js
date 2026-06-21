// Workers-shape handler (`export default { fetch }`) — the shape SvelteKit's
// adapter-cloudflare and Next's next-on-pages emit. This exercises the runtime
// layer (Web-API polyfill + node:* module shims) so you can validate it without
// scaffolding a full framework app:
//
//   go run ./tools/taubyte-ssr-adapter --mode fetch --node --framework worker \
//     --entry ./tools/taubyte-ssr-adapter/example/worker-example.js --out /tmp/w.zip
//   unzip -o /tmp/w.zip main.wasm -d /tmp
//   echo '{"method":"GET","url":"/"}' | wasmtime /tmp/main.wasm
//   echo '{"method":"POST","url":"/api/echo","headers":{"content-type":"application/x-www-form-urlencoded"},"body":"name=dylan"}' | wasmtime /tmp/main.wasm

import { AsyncLocalStorage } from "node:async_hooks";
import { EventEmitter } from "node:events";

const als = new AsyncLocalStorage();
const bus = new EventEmitter();

export default {
  async fetch(request) {
    const url = new URL(request.url);

    // FormData over a urlencoded POST body.
    if (url.pathname === "/api/echo" && request.method === "POST") {
      const fd = await request.formData();
      return Response.json({ got: fd.get("name") || null });
    }

    // node:async_hooks AsyncLocalStorage + node:events EventEmitter + crypto +
    // AbortController, all from the shim layer.
    return als.run({ id: crypto.randomUUID() }, () => {
      let events = 0;
      bus.on("hit", () => events++);
      bus.emit("hit");

      const ac = new AbortController();
      ac.abort();

      const ctx = als.getStore();
      return new Response(
        `<h1>Worker on Taubyte</h1>` +
          `<p>path: ${url.pathname}</p>` +
          `<p>reqId: ${ctx.id}</p>` +
          `<p>events: ${events}</p>` +
          `<p>aborted: ${ac.signal.aborted}</p>`,
        { headers: { "content-type": "text/html; charset=utf-8" } }
      );
    });
  },
};
