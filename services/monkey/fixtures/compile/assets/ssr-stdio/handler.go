// Command handler is a minimal WASI-stdio server bundle used to prove the
// substrate's wasi-stdio handler ABI end to end, independently of Javy: it is a
// plain WASI command (reads the request JSON from stdin, writes the response
// JSON to stdout), which is exactly the I/O shape a Javy/QuickJS bundle has.
//
// Build it to WebAssembly as a command module with TinyGo and drop the result
// next to this file as `main.wasm`; `website_ssr_stdio_test.go` picks it up.
// See README.md in this directory.
package main

import (
	"encoding/json"
	"io"
	"os"
)

type request struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
}

type response struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
}

func main() {
	in, _ := io.ReadAll(os.Stdin)

	var req request
	_ = json.Unmarshal(in, &req)

	// Render a path-dependent response so the test can prove the body is
	// produced by the bundle, not served statically.
	body := "STDIO rendered: " + req.URL
	if len(req.URL) >= 5 && req.URL[:5] == "/api/" {
		body = `{"stdio":true,"path":"` + req.URL + `"}`
	}

	out, _ := json.Marshal(response{
		Status:  200,
		Headers: map[string]string{"x-rendered-by": "taubyte-stdio"},
		Body:    body,
	})
	os.Stdout.Write(out)
}
