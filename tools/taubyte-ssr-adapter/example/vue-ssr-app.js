// Example: Vue 3 server-side rendering on the fetch tier (`--mode fetch --node`).
//
//   npm i vue
//   go run ./tools/taubyte-ssr-adapter --mode fetch --node --engine starlingmonkey \
//     --framework vue --entry ./example/vue-ssr-app.js --out handler.component.wasm
//   wasmtime serve -S cli=y handler.component.wasm
//
// renderToString runs on StarlingMonkey's native Web APIs. This is the same path
// Nuxt / SolidStart / Astro SSR take (a Web-standard fetch handler that renders).
// --node provides process.env.NODE_ENV, which Vue's bundle reads.

import { createSSRApp, h } from "vue";
import { renderToString } from "vue/server-renderer";

export default {
  async fetch(request) {
    const url = new URL(request.url);
    const app = createSSRApp({
      data: () => ({ path: url.pathname, items: ["one", "two", "three"] }),
      render() {
        return h("div", { id: "app" }, [
          h("h1", "Vue SSR on Taubyte"),
          h("p", "path: " + this.path),
          h("ul", this.items.map((i) => h("li", i))),
        ]);
      },
    });
    const html = await renderToString(app);
    return new Response("<!DOCTYPE html><html><body>" + html + "</body></html>", {
      headers: { "content-type": "text/html; charset=utf-8" },
    });
  },
};
