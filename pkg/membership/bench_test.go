package membership

import (
	"fmt"
	"testing"
	"time"

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

// BenchmarkStablePlacement is 50 members that have all been stable for a while, the
// answer a database call places from.
func BenchmarkStablePlacement(b *testing.B) {
	at := time.Unix(1_700_000_000, 0)
	s, err := New(Options{Stabilize: time.Minute, Now: func() time.Time { return at }})
	require.NoError(b, err)
	for at.Before(time.Unix(1_700_000_000, 0).Add(2 * time.Minute)) {
		for i := range 50 {
			require.NoError(b, s.Observe(Beat{Peer: fmt.Sprintf("p%02d", i), Incarnation: 1, Accepts: true}))
		}
		at = at.Add(time.Second)
	}
	require.Len(b, s.StablePlacement(), 50)
	b.ReportAllocs()
	for b.Loop() {
		s.StablePlacement()
	}
}
