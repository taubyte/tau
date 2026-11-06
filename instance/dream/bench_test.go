//go:build dreaming

package dream_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	nodeDream "github.com/taubyte/tau/instance/dream"
)

// BenchmarkARequestThroughTheGateway is the whole request path in one process: the
// gateway's handler, placement, the worker's resolve and pool, and a wasm function's
// warm call. Each package's own benchmark stops at a fake; this is what they add up to.
func BenchmarkARequestThroughTheGateway(b *testing.B) {
	defer nodeDream.Runtimes(nodeDream.Runtimes()...)
	r := deploy(b, "with/function/wasm", "hello.example.com", handlerWASM(b))
	handler := r.node.Handler()

	serve := func() int {
		req := httptest.NewRequest(http.MethodGet, "http://"+r.host+"/", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w.Code
	}
	if code := serve(); code != http.StatusOK {
		b.Fatalf("the function answered %d", code)
	}

	b.ReportAllocs()
	for b.Loop() {
		if code := serve(); code != http.StatusOK {
			b.Fatalf("the function answered %d", code)
		}
	}
}
