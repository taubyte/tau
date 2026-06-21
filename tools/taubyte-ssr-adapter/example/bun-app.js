// Example: a Bun app for `--mode bun` (Bun.serve, no framework).
//
//   go run ./tools/taubyte-ssr-adapter --mode bun --engine starlingmonkey \
//     --framework bun --entry ./example/bun-app.js --out handler.component.wasm
//   wasmtime serve -S cli=y handler.component.wasm   # then curl it
//
// Bun.serve's handler is a Web-standard fetch handler, so it runs directly on the
// component tier. The `Bun` global is provided by the adapter; Bun.serve captures
// the handler (no socket is bound) and each wasi:http request is driven through
// it. Config/secrets arrive via process.env / Bun.env; there is no fs/net.

const server = Bun.serve({
  port: 3000,
  fetch(req, server) {
    const url = new URL(req.url);

    if (url.pathname === "/") {
      return new Response("<h1>Bun.serve on Taubyte</h1>", { headers: { "content-type": "text/html" } });
    }

    if (url.pathname === "/api/info") {
      return Response.json
        ? Response.json({ ok: true, path: url.pathname, method: req.method })
        : new Response(JSON.stringify({ ok: true, path: url.pathname, method: req.method }), { headers: { "content-type": "application/json" } });
    }

    if (req.method === "POST" && url.pathname === "/api/echo") {
      return req.json().then((body) =>
        new Response(JSON.stringify({ echoed: body, greeting: Bun.env.GREETING || null }), { headers: { "content-type": "application/json" } })
      );
    }

    return new Response("not found", { status: 404 });
  },
});

console.log("bun server ready on", server.port);
