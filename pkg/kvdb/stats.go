package kvdb

import (
	"context"
	"fmt"

	"github.com/ipfs/go-cid"
	"github.com/taubyte/tau/core/kvdb"
	pb "github.com/taubyte/tau/pkg/kvdb/pb"
	"github.com/taubyte/tau/pkg/proto/codec"
)

func (kvd *kvDatabase) Stats(ctx context.Context) kvdb.Stats {
	s := kvd.datastore.InternalStats(ctx)
	heads := make([]cid.Cid, len(s.Heads))
	for i, h := range s.Heads {
		heads[i] = h.Cid
	}
	return &stats{
		heads:      heads,
		maxHeight:  s.MaxHeight,
		queuedJobs: s.QueuedJobs,
	}
}

func NewStats() kvdb.Stats {
	return &stats{}
}

func (s *stats) Type() kvdb.Type {
	return kvdb.TypeCRDT
}

func (s *stats) Heads() []cid.Cid {
	return s.heads
}

func (s *stats) Encode() []byte {
	m := pb.Stats{
		Heads:      make([][]byte, len(s.heads)),
		MaxHeight:  s.maxHeight,
		QueuedJobs: int64(s.queuedJobs),
	}
	for i, c := range s.heads {
		m.Heads[i] = c.Bytes()
	}
	// MarshalVT fails only on a message too large to size, which stats never is.
	data, _ := m.MarshalVT()
	return data
}

func (s *stats) Decode(data []byte) error {
	var m pb.Stats
	if err := codec.Decode(data, &m); err != nil {
		return fmt.Errorf("decoding stats data failed with %w", err)
	}

	s.maxHeight = m.MaxHeight
	s.queuedJobs = int(m.QueuedJobs)

	s.heads = make([]cid.Cid, len(m.Heads))
	for i, headBytes := range m.Heads {
		c, err := cid.Cast(headBytes)
		if err != nil {
			return fmt.Errorf("parsing cid failed with %w", err)
		}
		s.heads[i] = c
	}

	return nil
}
