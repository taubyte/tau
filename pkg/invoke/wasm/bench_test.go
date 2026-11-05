package wasm_test

import (
	"os"
	"strconv"
	"testing"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/invoke/wasm"
)

// benchTier is the handler fixture on a wasm tier in mode.
func benchTier(b *testing.B, mode wasm.Mode) *wasm.Runtime {
	b.Helper()
	code, err := os.ReadFile("fixtures/handler.wasm")
	if err != nil {
		b.Fatal(err)
	}
	r, err := wasm.New(&store{content: code}, wasm.WithMode(mode))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { r.Close() })
	return r
}

// A WARM CALL is what a request costs once its instance is up: the request lowered
// into the guest, the guest's answer lifted out.
func BenchmarkServeWarm(b *testing.B) {
	for _, mode := range []wasm.Mode{wasm.ModeCompiler, wasm.ModeInterpreter} {
		for _, size := range []int{0, 1 << 10, 64 << 10} {
			b.Run(string(mode)+"/"+sizeName(size), func(b *testing.B) {
				r := benchTier(b, mode)
				instance, err := r.Start(b.Context(), invoke.Spec{Name: "shop/prod/function/checkout", Artifact: "bafycomponent"})
				if err != nil {
					b.Fatal(err)
				}
				b.Cleanup(func() { instance.Close() })
				req := &invoke.Request{
					Method:  "POST",
					Path:    "/orders",
					Headers: map[string][]string{"x-trace": {"abc"}},
					Body:    make([]byte, size),
				}
				b.SetBytes(int64(size))
				b.ReportAllocs()
				for b.Loop() {
					if _, err := instance.Serve(b.Context(), req); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

// A COLD START is what the first request of an instance pays: the component
// instantiated from a compiled module the tier already holds, then closed.
func BenchmarkStartCold(b *testing.B) {
	for _, mode := range []wasm.Mode{wasm.ModeCompiler, wasm.ModeInterpreter} {
		b.Run(string(mode), func(b *testing.B) {
			r := benchTier(b, mode)
			spec := invoke.Spec{Name: "shop/prod/function/checkout", Artifact: "bafycomponent"}
			first, err := r.Start(b.Context(), spec)
			if err != nil {
				b.Fatal(err)
			}
			first.Close()
			b.ReportAllocs()
			for b.Loop() {
				instance, err := r.Start(b.Context(), spec)
				if err != nil {
					b.Fatal(err)
				}
				instance.Close()
			}
		})
	}
}

func sizeName(n int) string {
	switch {
	case n == 0:
		return "empty"
	case n >= 1<<10:
		return strconv.Itoa(n>>10) + "KiB"
	}
	return strconv.Itoa(n) + "B"
}
