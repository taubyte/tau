package js_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"warpjs/engine"

	js "github.com/taubyte/tau/pkg/invoke/js"
)

const workers = `
let calls = 0;
export default {
  async fetch(request) {
    calls++;
    const body = await request.text();
    return new Response(request.method + " " + body.length + " " + calls, { headers: { "x-calls": String(calls) } });
  },
};
`

// bundle is a module of about 150 KiB, the size the warpjs survey measured:
// stage 1 compiles it from source in every new realm.
func bundle() string {
	var b strings.Builder
	for n := range 2500 {
		fmt.Fprintf(&b, "export function f%d(a, b) { const x = a * %d + b; return x > 0 ? x - 1 : x + 1; }\n", n, n)
	}
	b.WriteString(workers)
	return b.String()
}

func modes() []js.Mode {
	out := []js.Mode{js.ModeBytecode}
	if engine.NativeSupported() {
		out = append(out, js.ModeNative, js.ModeJIT)
	}
	return out
}

// BenchmarkColdStart is Start and the first request: what a request that finds
// nothing warm pays.
func BenchmarkColdStart(b *testing.B) {
	for _, size := range []struct {
		name, src string
	}{{"small", workers}, {"150KiB", bundle()}} {
		for _, mode := range modes() {
			b.Run(size.name+"/"+string(mode), func(b *testing.B) {
				f := newFixture(b, size.src, js.WithMode(mode))
				req := get("/")
				b.ResetTimer()
				for range b.N {
					inst, err := f.tier.Start(context.Background(), f.spec)
					if err != nil {
						b.Fatal(err)
					}
					if _, err := inst.Serve(context.Background(), req); err != nil {
						b.Fatal(err)
					}
					inst.Close()
				}
			})
		}
	}
}

// trivial is the least a handler can do, so what it costs is the platform's
// own share of a request.
const trivial = `export default { fetch() { return new Response("ok"); } };`

// BenchmarkWarmCall is one request to a warm instance.
func BenchmarkWarmCall(b *testing.B) {
	for _, handler := range []struct{ name, src string }{{"trivial", trivial}, {"workers", workers}} {
		for _, mode := range modes() {
			b.Run(handler.name+"/"+string(mode), func(b *testing.B) {
				f := newFixture(b, handler.src, js.WithMode(mode))
				inst := f.start()
				req := get("/")
				b.ResetTimer()
				for range b.N {
					if _, err := inst.Serve(context.Background(), req); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
