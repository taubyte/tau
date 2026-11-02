package queue

import (
	"fmt"

	"github.com/taubyte/tau/pkg/proto/codec"
	queuev2 "github.com/taubyte/tau/pkg/proto/gen/tau/queue/v2"
)

// record is what a ready or claimed key holds: the payload, so a transition never
// reads a second key, and the attempt count. Stored as queuev2.Record.
type record struct {
	ID        string
	Data      []byte
	Attempts  uint32
	DeliverAt int64
	Deadline  int64
	Token     string
}

func (r record) encode() ([]byte, error) {
	m := queuev2.Record{Id: r.ID, Data: r.Data, Attempts: r.Attempts, DeliverAt: r.DeliverAt, Deadline: r.Deadline, Token: r.Token}
	return m.MarshalVT()
}

func decodeRecord(key string, raw []byte) (record, error) {
	var m queuev2.Record
	if err := codec.Decode(raw, &m); err != nil {
		return record{}, fmt.Errorf("%w at %q: %w", ErrCorruptRecord, key, err)
	}
	if m.Id == "" {
		return record{}, fmt.Errorf("%w at %q: no id", ErrCorruptRecord, key)
	}
	return record{ID: m.Id, Data: m.Data, Attempts: m.Attempts, DeliverAt: m.DeliverAt, Deadline: m.Deadline, Token: m.Token}, nil
}
