package kvdb

import (
	"context"

	"github.com/ipfs/go-cid"
	"github.com/taubyte/tau/core/kvdb"
)

func (kvd *kvDatabase) Stats(ctx context.Context) kvdb.Stats {
	s := kvd.datastore.InternalStats(ctx)
	heads := make([]cid.Cid, len(s.Heads))
	for i, h := range s.Heads {
		heads[i] = h.Cid
	}
	return &stats{heads: heads}
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
