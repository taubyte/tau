package stats

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// benchView is 50 nodes serving 20 names each, every name served by 25 of them.
func benchView(b *testing.B) *View {
	v, err := New(Options{})
	require.NoError(b, err)
	for i := range 50 {
		serving := make([]string, 20)
		for j := range serving {
			serving[j] = fmt.Sprintf("name%d.example.com", (i+j)%40)
		}
		require.NoError(b, v.Observe(Report{Peer: fmt.Sprintf("p%02d", i), Load: float64(i%10) / 10,
			Ready: true, Serving: serving, Addresses: []string{"203.0.113.1"}}))
	}
	return v
}

func BenchmarkRoute(b *testing.B) {
	v := benchView(b)
	b.ReportAllocs()
	for b.Loop() {
		v.Route("name7.example.com", time.Minute)
	}
}

// BenchmarkRoutePeers is the same read as placement makes it: peers alone, into a
// buffer the caller holds.
func BenchmarkRoutePeers(b *testing.B) {
	v := benchView(b)
	dst := make([]string, 0, 64)
	b.ReportAllocs()
	for b.Loop() {
		dst = v.RoutePeers("name7.example.com", time.Minute, dst[:0])
	}
}

func BenchmarkObserve(b *testing.B) {
	v, err := New(Options{})
	require.NoError(b, err)
	r := Report{Peer: "p", Load: 0.3, Ready: true, Serving: []string{"a", "b", "c"}, Addresses: []string{"203.0.113.1"}}
	b.ReportAllocs()
	for b.Loop() {
		require.NoError(b, v.Observe(r))
	}
}
