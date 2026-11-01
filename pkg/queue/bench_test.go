package queue

import (
	"strconv"
	"testing"
	"time"

	"github.com/taubyte/tau/pkg/raft"
)

func BenchmarkPushClaimAck(b *testing.B) {
	now := time.Unix(1_700_000_000, 0)
	group, err := NewGroup(raft.NewMockCluster(), WithClock(func() time.Time { return now }))
	if err != nil {
		b.Fatal(err)
	}
	q, err := group.Open(Config{Name: "build"})
	if err != nil {
		b.Fatal(err)
	}
	data := make([]byte, 128)
	b.ReportAllocs()
	i := 0
	for b.Loop() {
		i++
		if err := q.Push("job-"+strconv.Itoa(i), data); err != nil {
			b.Fatal(err)
		}
		d, err := q.Claim()
		if err != nil {
			b.Fatal(err)
		}
		if err := q.Ack(d); err != nil {
			b.Fatal(err)
		}
	}
}
