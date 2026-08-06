package packer

import (
	"bytes"
	"encoding/binary"
	"io"
	"testing"
)

var testMagic = Magic{0x01, 0xec}

const testVersion Version = 1

// frame builds a header with an arbitrary length field, which is what a remote
// peer controls. The declared length deliberately need not match the payload.
func frame(_type Type, length int64, payload []byte) []byte {
	var b bytes.Buffer
	b.Write(testMagic[:])
	binary.Write(&b, binary.LittleEndian, testVersion)
	binary.Write(&b, binary.LittleEndian, _type)
	binary.Write(&b, binary.LittleEndian, length)
	binary.Write(&b, binary.LittleEndian, Channel(0))
	b.Write(payload)
	return b.Bytes()
}

// TestHostileFrameLength: the length field is read off the wire as a signed
// int64. A negative one used to reach make([]byte, length) and panic the
// process — there is no recover() on the libp2p stream handler path, so any
// peer able to open a stream could kill a node with a 14-byte frame.
func TestHostileFrameLength(t *testing.T) {
	for _, tc := range []struct {
		name   string
		_type  Type
		length int64
	}{
		{"negative close", TypeClose, -1},
		{"negative data", TypeData, -1},
		{"min int64 close", TypeClose, -1 << 63},
		{"oversized close", TypeClose, 1 << 40},
		// No oversized-data case: the data path streams through
		// io.Copy(w, io.LimitReader(r, length)) and never allocates on the
		// claimed length, so a large one costs nothing until the bytes
		// actually arrive. Only the close path buffers.
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := New(testMagic, testVersion)
			raw := frame(tc._type, tc.length, nil)

			// Both entry points parse a header; both must survive.
			if _, _, err := p.Recv(bytes.NewReader(raw), io.Discard); err == nil {
				t.Error("Recv accepted a hostile length")
			}
			if _, _, err := p.Next(bytes.NewReader(raw)); err == nil {
				t.Error("Next accepted a hostile length")
			}
		})
	}
}

// A well-formed frame still round-trips.
func TestFrameRoundTrip(t *testing.T) {
	p := New(testMagic, testVersion)

	var sent bytes.Buffer
	payload := []byte("hello")
	if err := p.Send(3, &sent, bytes.NewReader(payload), int64(len(payload))); err != nil {
		t.Fatal(err)
	}

	var got bytes.Buffer
	channel, n, err := p.Recv(bytes.NewReader(sent.Bytes()), &got)
	if err != nil {
		t.Fatal(err)
	}
	if channel != 3 || n != int64(len(payload)) || got.String() != string(payload) {
		t.Fatalf("round trip: channel=%d n=%d body=%q", channel, n, got.String())
	}

	// A close frame carrying a real error still surfaces it.
	msg := []byte("upstream went away")
	closed := frame(TypeClose, int64(len(msg)), msg)
	if _, _, err = p.Recv(bytes.NewReader(closed), io.Discard); err == nil {
		t.Fatal("expected the close error to surface")
	}
	if _, _, err = p.Next(bytes.NewReader(closed)); err == nil {
		t.Fatal("expected the close error to surface via Next")
	}

	// An empty close is still EOF, not an error.
	if _, _, err := p.Recv(bytes.NewReader(frame(TypeClose, 0, nil)), io.Discard); err != io.EOF {
		t.Fatalf("empty close: got %v, want io.EOF", err)
	}

	// A close message at the cap is accepted; one byte over is refused before
	// anything is allocated.
	atCap := frame(TypeClose, MaxCloseMessageSize, bytes.Repeat([]byte("x"), MaxCloseMessageSize))
	if _, _, err := p.Recv(bytes.NewReader(atCap), io.Discard); err == nil {
		t.Fatal("expected the close error to surface at the cap")
	}
	if _, _, err := p.Recv(bytes.NewReader(frame(TypeClose, MaxCloseMessageSize+1, nil)), io.Discard); err == nil {
		t.Fatal("accepted a close message over the cap")
	}
}
