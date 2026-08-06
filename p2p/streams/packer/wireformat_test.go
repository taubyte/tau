package packer

import (
	"bytes"
	"encoding/hex"
	"errors"
	"io"
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
}{
	{
		"data/empty",
		func(p *packer, w io.Writer) error { return p.Send(0, w, bytes.NewReader(nil), 0) },
		"abcd070000000000000000000000",
	},
	{
		"data/hello/ch0",
		func(p *packer, w io.Writer) error { return p.Send(0, w, bytes.NewReader([]byte("hello")), 5) },
		"abcd07000005000000000000000068656c6c6f",
	},
	{
		"data/hello/ch255",
		func(p *packer, w io.Writer) error { return p.Send(255, w, bytes.NewReader([]byte("hello")), 5) },
		"abcd0700000500000000000000ff68656c6c6f",
	},
	{
		"data/binary/ch5",
		func(p *packer, w io.Writer) error {
			return p.Send(5, w, bytes.NewReader([]byte{0x00, 0xff, 0x7f, 0x80}), 4)
		},
		"abcd07000004000000000000000500ff7f80",
	},
	{
		"close/nil",
		func(p *packer, w io.Writer) error { return p.SendClose(3, w, nil) },
		"abcd070001000000000000000003",
	},
	{
		"close/eof",
		func(p *packer, w io.Writer) error { return p.SendClose(3, w, io.EOF) },
		"abcd070001000000000000000003",
	},
	{
		"close/err",
		func(p *packer, w io.Writer) error { return p.SendClose(9, w, errors.New("boom")) },
		"abcd070001040000000000000009626f6f6d",
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

// TestWireFormatDecode: what older nodes emit, we still read. Same vectors,
// driven through the parser instead of the writer.
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

			// Drain every frame in the vector; the stream case holds four.
			for r.Len() > 0 {
				if _, _, err := p.Recv(r, &out); err != nil && err != io.EOF {
					// A close frame carrying a message reports it as an error;
					// that is the intended signal, not a decode failure.
					if !bytes.Contains([]byte(err.Error()), []byte("packer close error")) {
						t.Fatalf("decoding a legacy frame failed: %v", err)
					}
				}
			}
		})
	}
}

// The stream vector must decode to exactly the bytes that went in.
func TestWireFormatStreamPayload(t *testing.T) {
	raw, err := hex.DecodeString(goldenFrames[len(goldenFrames)-1].hex)
	if err != nil {
		t.Fatal(err)
	}

	p := goldenPacker()
	r := bytes.NewReader(raw)
	var out bytes.Buffer
	for {
		if _, _, err := p.Recv(r, &out); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
	}

	if out.String() != "abcdefgh" {
		t.Fatalf("stream payload: got %q, want %q", out.String(), "abcdefgh")
	}
}
