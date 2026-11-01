package membership

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func BenchmarkObserve(b *testing.B) {
	s, err := New(Options{})
	require.NoError(b, err)
	beat := Beat{Peer: "p", Incarnation: 1, Accepts: true}
	b.ReportAllocs()
	for b.Loop() {
		require.NoError(b, s.Observe(beat))
	}
}

func BenchmarkStablePlacement(b *testing.B) {
	s, err := New(Options{})
	require.NoError(b, err)
	for i := range 50 {
		require.NoError(b, s.Observe(Beat{Peer: fmt.Sprintf("p%02d", i), Incarnation: 1, Accepts: true}))
	}
	b.ReportAllocs()
	for b.Loop() {
		s.StablePlacement()
	}
}
