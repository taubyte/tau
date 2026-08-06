package packer

import (
	"bytes"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"testing"
)

// The wire format is spoken by every node in a cloud, so a frame this build
// emits must stay byte-identical to one an older build emits — a node cannot
// tell who it is talking to. These vectors were captured from the original
// field-at-a-time implementation; they are the compatibility contract, not a
// snapshot of current behaviour to be re-recorded when it changes.
//
// Layout, confirmed against the bytes below:
//
//	magic[0:2] version[2:4] type[4] length[5:13] channel[13] payload...
var goldenFrames = []struct {
	name  string
	build func(p *packer, w io.Writer) error
	hex   string
	// Decoded expectations. Asserting the bytes back out is the point: a
	// decoder that consumed the right number of wire bytes and dropped the
	// payload would satisfy a test that only checked for the absence of an
	// error, and would still be broken.
	wantCh      Channel
	wantPayload []byte
	wantClose   string
}{
	{
		"data/empty",
		func(p *packer, w io.Writer) error { return p.Send(0, w, bytes.NewReader(nil), 0) },
		"abcd070000000000000000000000",
		0, nil, "",
	},
	{
		"data/hello/ch0",
		func(p *packer, w io.Writer) error { return p.Send(0, w, bytes.NewReader([]byte("hello")), 5) },
		"abcd07000005000000000000000068656c6c6f",
		0, []byte("hello"), "",
	},
	{
		"data/hello/ch255",
		func(p *packer, w io.Writer) error { return p.Send(255, w, bytes.NewReader([]byte("hello")), 5) },
		"abcd0700000500000000000000ff68656c6c6f",
		255, []byte("hello"), "",
	},
	{
		"data/binary/ch5",
		func(p *packer, w io.Writer) error {
			return p.Send(5, w, bytes.NewReader([]byte{0x00, 0xff, 0x7f, 0x80}), 4)
		},
		"abcd07000004000000000000000500ff7f80",
		5, []byte{0x00, 0xff, 0x7f, 0x80}, "",
	},
	{
		"close/nil",
		func(p *packer, w io.Writer) error { return p.SendClose(3, w, nil) },
		"abcd070001000000000000000003",
		3, nil, "",
	},
	{
		"close/eof",
		func(p *packer, w io.Writer) error { return p.SendClose(3, w, io.EOF) },
		"abcd070001000000000000000003",
		3, nil, "",
	},
	{
		"close/err",
		func(p *packer, w io.Writer) error { return p.SendClose(9, w, errors.New("boom")) },
		"abcd070001040000000000000009626f6f6d",
		9, nil, "boom",
	},
	{
		// Three data frames plus the trailing close, all in one stream.
		"stream/3chunks",
		func(p *packer, w io.Writer) error {
			if _, err := p.Stream(2, w, bytes.NewReader([]byte("abcdefgh")), 3); err != io.EOF {
				return err
			}
			return nil
		},
		"abcd070000030000000000000002616263" +
			"abcd070000030000000000000002646566" +
			"abcd0700000200000000000000026768" +
			"abcd070001000000000000000002",
		2, []byte("abcdefgh"), "",
	},
}

func goldenPacker() *packer { return New(Magic{0xAB, 0xCD}, Version(7)).(*packer) }

// TestWireFormatEncode: what we emit is what older nodes expect.
func TestWireFormatEncode(t *testing.T) {
	for _, tc := range goldenFrames {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := tc.build(goldenPacker(), &buf); err != nil {
				t.Fatal(err)
			}
			if got := hex.EncodeToString(buf.Bytes()); got != tc.hex {
				t.Errorf("wire format changed\n got: %s\nwant: %s", got, tc.hex)
			}
		})
	}
}

// TestWireFormatDecode: what older nodes emit, we still read — and read
// correctly. Every vector is checked for channel, byte count and payload
// content, not merely for the absence of an error.
func TestWireFormatDecode(t *testing.T) {
	for _, tc := range goldenFrames {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := hex.DecodeString(tc.hex)
			if err != nil {
				t.Fatal(err)
			}

			p := goldenPacker()
			r := bytes.NewReader(raw)
			var out bytes.Buffer
			var total int64
			var gotClose string
			sawClose := false

			// Drain every frame in the vector; the stream case holds four.
			for r.Len() > 0 {
				ch, n, err := p.Recv(r, &out)

				if ch != tc.wantCh {
					t.Fatalf("channel: got %d, want %d", ch, tc.wantCh)
				}

				switch {
				case err == io.EOF:
					// An empty close frame. End of this vector.
					sawClose = true
				case err != nil:
					// A close frame carrying a message surfaces it as an
					// error; that is the signal, not a decode failure.
					sawClose = true
					gotClose = err.Error()
				default:
					total += n
				}
			}

			if got := out.Bytes(); !bytes.Equal(got, tc.wantPayload) {
				t.Errorf("payload: got %q, want %q", got, tc.wantPayload)
			}

			if total != int64(len(tc.wantPayload)) {
				t.Errorf("reported length: got %d, want %d", total, len(tc.wantPayload))
			}

			if tc.wantClose != "" {
				if !sawClose {
					t.Fatal("expected a close frame, saw none")
				}
				if !strings.Contains(gotClose, tc.wantClose) {
					t.Errorf("close message: got %q, want it to contain %q", gotClose, tc.wantClose)
				}
			} else if gotClose != "" {
				t.Errorf("unexpected close message: %q", gotClose)
			}
		})
	}
}
