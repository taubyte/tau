# Hono server-bundle fixture

`website_hono_test.go` proves a real **Hono** app renders through the substrate
(static + dynamic + `/api`), using the adapter's `--mode fetch` Web-API polyfill
+ Javy. It is gated on a prebuilt `main.wasm` here (not committed — it's a JS
build).

Build it from the Hono example:

```sh
cd tools/taubyte-ssr-adapter/example && npm i hono && cd -

go run ./tools/taubyte-ssr-adapter --mode fetch --framework hono \
  --entry ./tools/taubyte-ssr-adapter/example/hono-app.js --out /tmp/h.zip

unzip -o /tmp/h.zip main.wasm -d services/monkey/fixtures/compile/assets/hono/

go test -tags dreaming -run TestWebsiteHono_Dreaming -v ./services/monkey/fixtures/compile/
```

Requires `esbuild` + `javy` (build) and a working dream universe (test). Without
`main.wasm` the test skips.
