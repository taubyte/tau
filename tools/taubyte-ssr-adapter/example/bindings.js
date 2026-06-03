// StarlingMonkey component using Taubyte bindings. Build with --engine
// starlingmonkey; the substrate injects secrets and a binding endpoint per
// request (see the component shim), surfaced on the Workers-style `env`:
//
//   env.<NAME>   secrets/config declared for the website
//   env.KV       get(key)/put(key,val)/delete(key)/list(prefix)
//   env.STORAGE  get(path)/put(path,body)
//   env.ASSETS   fetch(req) — static assets (404 standalone; served by the
//                static layer in production)
//
//   go run ./tools/taubyte-ssr-adapter --mode fetch --engine starlingmonkey \
//     --framework custom --entry ./example/bindings.js --out handler.component.wasm
export default {
  async fetch(request, env) {
    const url = new URL(request.url);

    // A counter persisted in KV.
    if (url.pathname === "/hit") {
      const n = Number((await env.KV.get("hits")) || 0) + 1;
      await env.KV.put("hits", String(n));
      return Response.json({ hits: n });
    }

    // Echo a secret (never do this for real — illustration only).
    if (url.pathname === "/whoami") {
      return Response.json({ region: env.REGION || "unknown", hasSecret: !!env.API_KEY });
    }

    return new Response(
      `<h1>Bindings demo</h1><p>try <code>/hit</code> (KV) and <code>/whoami</code> (secrets)</p>`,
      { headers: { "content-type": "text/html; charset=utf-8" } }
    );
  },
};
