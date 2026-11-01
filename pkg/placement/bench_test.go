package placement

import (
	"fmt"
	"testing"
)

var (
	sinkIDs   []string
	sinkBool  bool
	sinkScore uint64
)

func BenchmarkDesired(b *testing.B) {
	members := make([]Member, 50)
	for i := range members {
		members[i] = Member{ID: fmt.Sprintf("12D3KooWpeer%03d", i), Accepts: i%7 != 0}
	}
	b.ReportAllocs()
	for b.Loop() {
		sinkIDs = Desired("index/s042", members, 3)
	}
}

func BenchmarkHolds(b *testing.B) {
	members := make([]Member, 50)
	for i := range members {
		members[i] = Member{ID: fmt.Sprintf("12D3KooWpeer%03d", i), Accepts: true}
	}
	b.ReportAllocs()
	for b.Loop() {
		sinkBool = Holds("index/s042", members, 3, members[10].ID)
	}
}

func BenchmarkScore(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		sinkScore = Score("index/s042", "12D3KooWpeer001")
	}
}
