package kvdb

import (
	"fmt"

	"github.com/fxamacker/cbor/v2"
	"github.com/ipfs/go-cid"
	"github.com/taubyte/tau/core/kvdb"
)

func (kvd *kvDatabase) Stats() kvdb.Stats {
	s := kvd.datastore.InternalStats()
	return &stats{
		heads:      s.Heads,
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
	encoded := statsCbor{
		Heads:      make([][]byte, 0, len(s.heads)),
		MaxHeight:  s.maxHeight,
		QueuedJobs: s.queuedJobs,
	}

	for _, c := range s.heads {
		encoded.Heads = append(encoded.Heads, c.Bytes())
	}

	data, _ := cbor.Marshal(encoded)

	return data
}

func (s *stats) Decode(data []byte) error {
	var decoded statsCbor
	err := cbor.Unmarshal(data, &decoded)
	if err != nil {
		return fmt.Errorf("decoding stats data failed with %w", err)
	}

	s.maxHeight = decoded.MaxHeight
	s.queuedJobs = decoded.QueuedJobs

	s.heads = make([]cid.Cid, len(decoded.Heads))
	for i, headBytes := range decoded.Heads {
		c, err := cid.Cast(headBytes)
		if err != nil {
			return fmt.Errorf("parsing cid failed with %w", err)
		}
		s.heads[i] = c
	}

	return nil
}
