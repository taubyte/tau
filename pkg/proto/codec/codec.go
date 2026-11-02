// Package codec is the protobuf codec every tau connect handler and client uses.
package codec

import (
	"fmt"
	"slices"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
)

// Codec is connect's binary proto codec over vtprotobuf's generated methods
// (SizeVT, MarshalToSizedBufferVT, UnmarshalVT), so a message tau generated never
// goes through protobuf-go's reflection. A message without them falls back to
// protobuf-go. It keeps connect's name, "proto", so it replaces the default on
// both ends and the wire is unchanged.
//
// A string is whatever bytes it holds: vtproto, unlike protobuf-go, does not refuse
// invalid UTF-8, and every peer tau talks to is tau.
var Codec = codec{}

// Option is Codec as a handler or client option. Every handler and client tau
// builds takes it; TestEveryConnectSiteTakesTheCodec holds them to it.
func Option() connect.Option { return connect.WithCodec(Codec) }

type codec struct{}

type vtMarshaler interface {
	SizeVT() int
	MarshalToSizedBufferVT([]byte) (int, error)
}

type vtUnmarshaler interface {
	UnmarshalVT([]byte) error
}

func (codec) Name() string { return "proto" }

func (codec) IsBinary() bool { return true }

func (c codec) Marshal(m any) ([]byte, error) { return c.MarshalAppend(nil, m) }

// MarshalAppend lets connect hand over a pooled buffer.
func (codec) MarshalAppend(dst []byte, m any) ([]byte, error) {
	if v, ok := m.(vtMarshaler); ok {
		n := v.SizeVT()
		dst = slices.Grow(dst, n)
		end := len(dst) + n
		if _, err := v.MarshalToSizedBufferVT(dst[len(dst):end]); err != nil {
			return nil, err
		}
		return dst[:end], nil
	}
	msg, ok := m.(proto.Message)
	if !ok {
		return nil, fmt.Errorf("codec: %T is not a proto message", m)
	}
	return proto.MarshalOptions{}.MarshalAppend(dst, msg)
}

// MarshalStable is protobuf-go's deterministic output: vtproto writes a map in
// the order Go ranges it.
func (codec) MarshalStable(m any) ([]byte, error) {
	msg, ok := m.(proto.Message)
	if !ok {
		return nil, fmt.Errorf("codec: %T is not a proto message", m)
	}
	return proto.MarshalOptions{Deterministic: true}.Marshal(msg)
}

// Unmarshal resets the message first: UnmarshalVT merges into what is there,
// where proto.Unmarshal replaces it.
func (codec) Unmarshal(data []byte, m any) error {
	if v, ok := m.(vtUnmarshaler); ok {
		if r, ok := m.(interface{ Reset() }); ok {
			r.Reset()
		}
		if err := v.UnmarshalVT(data); err != nil {
			return fmt.Errorf("codec: unmarshal into %T: %w", m, err)
		}
		return nil
	}
	msg, ok := m.(proto.Message)
	if !ok {
		return fmt.Errorf("codec: %T is not a proto message", m)
	}
	if err := proto.Unmarshal(data, msg); err != nil {
		return fmt.Errorf("codec: unmarshal into %T: %w", m, err)
	}
	return nil
}
