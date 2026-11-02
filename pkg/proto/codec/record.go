package codec

import (
	"errors"

	"google.golang.org/protobuf/proto"
)

// ErrNotTheRecord is bytes that are not the record they were read as. Protobuf keeps a
// field it does not declare rather than refusing it, so a store reading its own rows
// refuses one, and a foreign or corrupt row stays an error.
var ErrNotTheRecord = errors.New("codec: bytes are not the record they were read as")

// Record is a message a store writes and reads back for itself.
type Record interface {
	proto.Message
	MarshalVT() ([]byte, error)
	UnmarshalVT([]byte) error
}

// Decode reads raw into m, which must be empty, refusing a field m does not declare.
// An empty raw is m's zero value: a record that cannot be empty checks its own fields.
func Decode(raw []byte, m Record) error {
	if err := m.UnmarshalVT(raw); err != nil {
		return err
	}
	if len(m.ProtoReflect().GetUnknown()) > 0 {
		return ErrNotTheRecord
	}
	return nil
}

// DecodeNonEmpty is Decode for a record that is never its zero value: an empty raw is
// refused rather than read as one.
func DecodeNonEmpty(raw []byte, m Record) error {
	if len(raw) == 0 {
		return ErrNotTheRecord
	}
	return Decode(raw, m)
}
