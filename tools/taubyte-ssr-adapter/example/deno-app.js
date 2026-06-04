// Example: a Deno app for `--mode deno` (Deno.serve).
//
//   go run ./tools/taubyte-ssr-adapter --mode deno --engine starlingmonkey \
//     --framework deno --entry ./example/deno-app.js --out handler.component.wasm
//   wasmtime serve -S cli=y handler.component.wasm
//
// Deno.serve's handler is a Web-standard fetch handler, so it runs directly on the
// component tier. The `Deno` global is provided by the adapter; Deno.serve captures
// the handler (no socket is bound). Config/secrets arrive via Deno.env (backed by
// process.env, which the substrate populates from x-taubyte-env); there is no fs/net.

Deno.serve((req) => {
  const url = new URL(req.url);

  if (url.pathname === "/") {
    return new Response("<h1>Deno.serve on Taubyte</h1>", { headers: { "content-type": "text/html" } });
  }
  if (url.pathname === "/api/info") {
    return Response.json({ ok: true, path: url.pathname, greeting: Deno.env.get("GREETING") || null });
  }
  if (req.method === "POST" && url.pathname === "/api/echo") {
    return req.json().then((body) => Response.json({ echoed: body }));
  }
  return new Response("not found", { status: 404 });
});
