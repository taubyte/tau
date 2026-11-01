package queue

import (
	"fmt"

	"github.com/fxamacker/cbor/v2"
)

// record is what a ready or claimed key holds: the payload, so a transition never
// reads a second key, and the attempt count. cbor with integer keys, as pkg/raft
// stores its values; it never crosses a wire.
type record struct {
	ID        string `cbor:"1,keyasint"`
	Data      []byte `cbor:"2,keyasint"`
	Attempts  uint32 `cbor:"3,keyasint"`
	DeliverAt int64  `cbor:"4,keyasint"`
	Deadline  int64  `cbor:"5,keyasint,omitempty"`
	Token     string `cbor:"6,keyasint,omitempty"`
}

func (r record) encode() ([]byte, error) {
	return cbor.Marshal(r)
}

func decodeRecord(key string, raw []byte) (record, error) {
	var r record
	if err := cbor.Unmarshal(raw, &r); err != nil {
		return record{}, fmt.Errorf("%w at %q: %w", ErrCorruptRecord, key, err)
	}
	if r.ID == "" {
		return record{}, fmt.Errorf("%w at %q: no id", ErrCorruptRecord, key)
	}
	return r, nil
}
