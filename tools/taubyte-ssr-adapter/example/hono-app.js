// Example Hono app — a Web-standard `fetch` handler. Build it with the adapter
// in fetch mode, which installs the Web API polyfill (web.js) so Hono's
// Request/Response/URL usage works on Javy:
//
//   npm i hono
//   go run ./tools/taubyte-ssr-adapter --mode fetch --framework hono \
//     --entry ./tools/taubyte-ssr-adapter/example/hono-app.js \
//     --out /tmp/handler.wasm.zip --manifest /tmp/ssr.json
//
// Then host the wasm via the wasi-stdio path (see README / the ssr-stdio test).

import { Hono } from "hono";

const app = new Hono();

app.get("/", (c) => c.html("<h1>Hello from Hono on Taubyte</h1>"));
app.get("/api/ping", (c) => c.json({ ok: true, runtime: "javy" }));
app.get("/blog/:slug", (c) => c.text("post: " + c.req.param("slug")));
app.post("/api/echo", async (c) => c.json({ youSent: await c.req.text() }));

export default app; // Hono exposes app.fetch(Request) -> Response
