// Example Taubyte SSR handler — the polyfill-free contract that runs on bare
// Javy. Default-export a function taking a JSON request and returning a JSON
// response (sync or async):
//
//   req : { method, url, headers, body }
//   res : { status, headers, body }
//
// Its output intentionally mirrors the Go stand-in in
// services/monkey/fixtures/compile/assets/ssr-stdio so you can validate the real
// Javy toolchain against the existing test:
//
//   go run ./tools/taubyte-ssr-adapter --entry ./tools/taubyte-ssr-adapter/example/app.js --out /tmp/h.zip
//   unzip -o /tmp/h.zip main.wasm -d services/monkey/fixtures/compile/assets/ssr-stdio/
//   go test -tags dreaming -run TestWebsiteSSRStdio_Dreaming -v ./services/monkey/fixtures/compile/

export default function handle(req) {
  if (req.url.startsWith("/api/")) {
    return {
      status: 200,
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ stdio: true, path: req.url }),
    };
  }

  return {
    status: 200,
    headers: { "content-type": "text/html; charset=utf-8" },
    body: "STDIO rendered: " + req.url,
  };
}
