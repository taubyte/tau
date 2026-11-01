package storekey_test

import (
	"testing"

	"github.com/taubyte/tau/pkg/storekey"
)

var sinkErr error

func BenchmarkCheck(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		sinkErr = storekey.Check("shop/prod/QmCache/", "session/abc123")
	}
}

func BenchmarkCanonical(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		sinkErr = storekey.Canonical("p/shop/prod")
	}
}
