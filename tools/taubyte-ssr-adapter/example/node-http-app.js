// Example: a Node HTTP-server app for `--mode node` (no framework, zero deps).
//
//   go run ./tools/taubyte-ssr-adapter --mode node --engine starlingmonkey \
//     --framework node --entry ./example/node-http-app.js \
//     --out handler.component.wasm
//   wasmtime serve -S cli=y handler.component.wasm   # then curl it
//
// The bridge captures the handler from createServer/listen() (no socket is
// bound) and drives each wasi:http request through it. This is HTTP
// request-handler compatibility — there is no fs/net; use Taubyte primitives.

import http from "node:http";

const server = http.createServer((req, res) => {
  if (req.method === "GET" && req.url === "/") {
    res.writeHead(200, { "content-type": "text/html" });
    res.end("<h1>node:http on Taubyte</h1>");
    return;
  }

  if (req.method === "GET" && req.url.startsWith("/api/info")) {
    res.writeHead(200, { "content-type": "application/json" });
    res.end(JSON.stringify({ ok: true, method: req.method, url: req.url }));
    return;
  }

  // Body parsing the classic way: 'data' + 'end' events.
  if (req.method === "POST" && req.url === "/api/echo") {
    const chunks = [];
    req.on("data", (c) => chunks.push(c));
    req.on("end", () => {
      const body = Buffer.concat(chunks.map((c) => (Buffer.isBuffer(c) ? c : Buffer.from(c)))).toString();
      res.writeHead(200, { "content-type": "application/json" });
      res.end(JSON.stringify({ echoed: body }));
    });
    return;
  }

  res.writeHead(404, { "content-type": "text/plain" });
  res.end("not found");
});

// listen()'s port is ignored (there is no socket); the ready callback still runs.
server.listen(3000, () => console.log("node http server ready"));
