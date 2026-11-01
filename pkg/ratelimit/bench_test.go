package ratelimit_test

import (
	"testing"
	"time"

	"github.com/taubyte/tau/pkg/ratelimit"
)

var sinkOK bool

func BenchmarkAllow(b *testing.B) {
	now := time.Unix(1_700_000_000, 0)
	l := ratelimit.NewBuckets(func() time.Time { return now }, 4096)
	b.ReportAllocs()
	for b.Loop() {
		sinkOK = l.Allow("ip/203.0.113.7", 600, 100)
	}
}

func BenchmarkGauge(b *testing.B) {
	g := ratelimit.NewGauge()
	b.ReportAllocs()
	for b.Loop() {
		leave, _ := g.Enter("203.0.113.0/24", 10)
		leave()
	}
}
