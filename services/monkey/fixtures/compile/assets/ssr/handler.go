// Package lib is a minimal Taubyte server bundle used to prove SSR serving end
// to end. It is an ordinary Taubyte HTTP function (`//export` + go-sdk event):
// the substrate runtime hosts it as a website's server bundle and calls it for
// every dynamic request.
//
// Build it to WebAssembly with the Taubyte Go toolchain (the `taubyte/go-wasi`
// image, the same one Monkey uses) or TinyGo, and drop the result next to this
// file as `main.wasm`; `website_ssr_test.go` picks it up automatically. See
// README.md in this directory.
package main

import (
	"github.com/taubyte/go-sdk/event"
)
func main() {}
//lint:ignore U1000 wasm export
//export ssrHandler
func ssrHandler(e event.Event) uint32 {
	h, err := e.HTTP()
	if err != nil {
		return 1
	}

	path, err := h.Path()
	if err != nil {
		return 1
	}

	// Render a path-dependent response so the test can prove the body is
	// produced on the server, not served from the static bundle. `/api/*` gets
	// JSON, everything else gets HTML.
	if len(path) >= 5 && path[:5] == "/api/" {
		h.Write([]byte(`{"ssr":true,"path":"` + path + `"}`))
		return 0
	}

	h.Write([]byte("<!doctype html><html><body><h1>SSR rendered: " + path + "</h1></body></html>"))
	return 0
}
