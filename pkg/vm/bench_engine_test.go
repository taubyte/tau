package vm

import (
	"context"
	"testing"
)

// BenchmarkEngineWarmCall isolates the engine's warm Go->wasm call+return path
// (no HTTP/p2p/TNS), the cleanest signal for an engine swap. toi32 is a trivial
// identity export, so the measurement is dominated by call dispatch.
func BenchmarkEngineWarmCall(b *testing.B) {
	funcs, err := newFuncs([]string{"toi32"})
	if err != nil {
		b.Fatal(err)
	}
	fn := funcs["toi32"]
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := fn.RawCall(ctx, 42); err != nil {
			b.Fatal(err)
		}
	}
}
