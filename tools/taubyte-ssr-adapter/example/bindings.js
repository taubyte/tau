// StarlingMonkey component using Taubyte named bindings. Build with
// --engine starlingmonkey. A website declares its bindings in config; each
// becomes env.<Name> (Workers-style):
//
//   bindings:
//     - { name: CACHE,   type: kv,      resource: /cache }     # env.CACHE
//     - { name: UPLOADS, type: storage, resource: /uploads }   # env.UPLOADS
//     - { name: API_KEY, type: secret,  resource: MYAPP_KEY }  # env.API_KEY
//
//   env.CACHE     get(key)/put(key,val)/delete(key)/list(prefix)
//   env.UPLOADS   get(path)/put(path,body)
//   env.API_KEY   the secret value (resolved from the node env var MYAPP_KEY)
//   env.ASSETS    fetch(req) — static assets (served by the static layer)
//
// With no bindings declared, env.KV and env.STORAGE are provided by default
// (mapped to resources matched by the website name).
//
//   go run ./tools/taubyte-ssr-adapter --mode fetch --engine starlingmonkey \
//     --framework custom --entry ./example/bindings.js --out handler.component.wasm
export default {
  async fetch(request, env) {
    const url = new URL(request.url);

    // A counter persisted in the CACHE kv binding.
    if (url.pathname === "/hit") {
      const n = Number((await env.CACHE.get("hits")) || 0) + 1;
      await env.CACHE.put("hits", String(n));
      return Response.json({ hits: n });
    }

    // A secret binding (never echo a real secret — illustration only).
    if (url.pathname === "/whoami") {
      return Response.json({ hasKey: !!env.API_KEY });
    }

    return new Response(
      `<h1>Bindings demo</h1><p>try <code>/hit</code> (KV) and <code>/whoami</code> (secret)</p>`,
      { headers: { "content-type": "text/html; charset=utf-8" } }
    );
  },
};
