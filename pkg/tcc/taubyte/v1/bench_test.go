package compiler

import (
	"context"
	"testing"
)

// BenchmarkCompile runs a full compile of the fixture project — read the config
// tree via yaseer + all four transform passes — the work a patrick build does.
func BenchmarkCompile(b *testing.B) {
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c, err := New(WithLocal("fixtures/config"), WithBranch("master"))
		if err != nil {
			b.Fatal(err)
		}
		if _, _, err := c.Compile(ctx); err != nil {
			b.Fatal(err)
		}
	}
}
