package packer

import (
	"encoding/binary"
	"fmt"
	"io"
	"sync"
)

var DefaultBufferSize = 32 * 1024

// bufPool is used to reuse buffers for Stream operations to reduce GC pressure
var bufPool = sync.Pool{
	New: func() any {
		buf := make([]byte, DefaultBufferSize)
		return &buf
	},
}

type packer struct {
	magic   Magic
	version Version
}

const (
	TypeData Type = iota
	TypeClose
)

type Magic [2]byte
type Channel uint8
type Type uint8
type Version uint16

type Packer interface {
	Send(Channel, io.Writer, io.Reader, int64) error
	Stream(Channel, io.Writer, io.Reader, int) (int64, error)
	Recv(io.Reader, io.Writer) (Channel, int64, error)
	Next(r io.Reader) (Channel, int64, error)
}

func New(magic Magic, version Version) Packer {
	p := &packer{
		version: version,
	}
	p.magic[0] = magic[0]
	p.magic[1] = magic[1]
	return p
}

// headerSize is magic(2) + version(2) + type(1) + length(8) + channel(1).
// The layout is the wire format and cannot change.
const headerSize = 14

// encodeHeader lays the header into a caller-owned array. Writing the fields
// one at a time cost five binary.Write calls — five heap allocations and,
// worse, five separate Writes to the stream, so every frame hit the transport
// six times instead of twice.
func (p packer) encodeHeader(hdr *[headerSize]byte, channel Channel, _type Type, length int64) {
	hdr[0], hdr[1] = p.magic[0], p.magic[1]
	binary.LittleEndian.PutUint16(hdr[2:4], uint16(p.version))
	hdr[4] = byte(_type)
	binary.LittleEndian.PutUint64(hdr[5:13], uint64(length))
	hdr[13] = byte(channel)
}

// copyPayload moves exactly length bytes from r to w. io.Copy would allocate
// its own buffer here — sized min(32KiB, length), since io.LimitReader tells
// it how much is coming — on every single frame. The pool makes that nothing.
// A zero length is not short-circuited: io.CopyBuffer still consults the
// destination's ReadFrom, and for a *bytes.Buffer that is what leaves an empty
// (rather than nil) result behind. Callers compare against []byte{}.
func copyPayload(w io.Writer, r io.Reader, length int64) (int64, error) {
	bufPtr := bufPool.Get().(*[]byte)
	defer bufPool.Put(bufPtr)

	return io.CopyBuffer(w, io.LimitReader(r, length), *bufPtr)
}

// sendBytes is send for a payload already in memory: no intermediate reader,
// no copy buffer, no pool round trip — just the header and the bytes.
func (p packer) sendBytes(channel Channel, _type Type, w io.Writer, payload []byte) error {
	var hdr [headerSize]byte
	p.encodeHeader(&hdr, channel, _type, int64(len(payload)))

	if _, err := w.Write(hdr[:]); err != nil {
		return fmt.Errorf("writing header failed: %w", err)
	}

	if len(payload) > 0 {
		if _, err := w.Write(payload); err != nil {
			return fmt.Errorf("writing payload failed: %w", err)
		}
	}

	return nil
}

func (p packer) send(channel Channel, _type Type, w io.Writer, r io.Reader, length int64) error {
	var hdr [headerSize]byte
	p.encodeHeader(&hdr, channel, _type, length)

	if _, err := w.Write(hdr[:]); err != nil {
		return fmt.Errorf("writing header failed: %w", err)
	}

	n, err := copyPayload(w, r, length)
	if n != length {
		return fmt.Errorf("short write: expected %d bytes, wrote %d: %w", length, n, io.ErrShortWrite)
	}

	if err != nil {
		return fmt.Errorf("copying data failed: %w", err)
	}

	return nil
}

func (p packer) Send(channel Channel, w io.Writer, r io.Reader, length int64) error {
	return p.send(channel, TypeData, w, r, length)
}

func (p packer) Stream(channel Channel, w io.Writer, r io.Reader, bufSize int) (int64, error) {
	var (
		err error
		n   int
		l   int64
	)

	defer func() {
		p.SendClose(channel, w, err)
	}()

	bufPtr := bufPool.Get().(*[]byte)
	buf := *bufPtr
	if len(buf) < bufSize {
		buf = make([]byte, bufSize)
		bufPtr = &buf
	} else {
		buf = buf[:bufSize]
	}
	defer bufPool.Put(bufPtr)

	for {
		n, err = r.Read(buf)
		l += int64(n)
		if n > 0 {
			// The chunk is already a []byte. Wrapping it in a fresh
			// bytes.Buffer per iteration only to copy it back out again was
			// an allocation and a memcpy per chunk.
			if err := p.sendBytes(channel, TypeData, w, buf[:n]); err != nil {
				return l, fmt.Errorf("failed to send body payload with %w", err)
			}
		}
		if err != nil {
			if err == io.EOF {
				return l, io.EOF
			}
			return l, fmt.Errorf("stream ended with %w", err)
		}
	}
}

func (p packer) SendClose(channel Channel, w io.Writer, err error) error {
	var msg []byte
	if err != nil && err != io.EOF {
		msg = []byte(err.Error())
	}

	return p.sendBytes(channel, TypeClose, w, msg)
}

// MaxCloseMessageSize bounds the error string a close frame can make us
// buffer. SendClose only ever writes err.Error(), so anything larger is
// malformed rather than merely long.
const MaxCloseMessageSize = 64 * 1024

// readHeader reads a frame header off the wire and validates the one field a
// peer can use against us. length is read as a signed 64-bit integer straight
// from the remote, and every frame is one: a negative value panics
// make([]byte, length) on the close path and silently truncates to nothing on
// the data path. Both call sites parsed this header themselves, so both had
// the bug; validating it here means a third caller cannot reintroduce it.
func (p packer) readHeader(r io.Reader) (_type Type, channel Channel, length int64, err error) {
	// One read for the whole header. Field-at-a-time binary.Read cost four
	// allocations and four round trips to the stream per frame.
	var hdr [headerSize]byte
	if _, err = io.ReadFull(r, hdr[:]); err != nil {
		return 0, 0, 0, fmt.Errorf("reading header failed: %w", err)
	}

	if hdr[0] != p.magic[0] || hdr[1] != p.magic[1] {
		return 0, 0, 0, fmt.Errorf("wrong packer magic: expected [%d %d], got [%d %d]", p.magic[0], p.magic[1], hdr[0], hdr[1])
	}

	if version := Version(binary.LittleEndian.Uint16(hdr[2:4])); version != p.version {
		return 0, 0, 0, fmt.Errorf("wrong packer version: expected %d, got %d", p.version, version)
	}

	_type = Type(hdr[4])
	length = int64(binary.LittleEndian.Uint64(hdr[5:13]))
	channel = Channel(hdr[13])

	if length < 0 {
		return 0, 0, 0, fmt.Errorf("negative payload length: %d", length)
	}

	return _type, channel, length, nil
}

// readClose turns a close frame into the error it carries. The length is
// bounded before it is allocated, so a peer cannot claim a gigabyte and have
// us reserve it before sending a single byte.
func (p packer) readClose(r io.Reader, channel Channel, length int64) (Channel, int64, error) {
	if length == 0 {
		return channel, 0, io.EOF
	}

	if length > MaxCloseMessageSize {
		return channel, 0, fmt.Errorf("close message too large: %d bytes", length)
	}

	errMsg := make([]byte, length)
	if _, err := io.ReadFull(r, errMsg); err != nil {
		return channel, 0, fmt.Errorf("failed to read error message: %w", err)
	}

	return channel, 0, fmt.Errorf("packer close error: %s", string(errMsg))
}

func (p packer) Recv(r io.Reader, w io.Writer) (Channel, int64, error) {
	_type, channel, length, err := p.readHeader(r)
	if err != nil {
		return 0, 0, err
	}

	switch _type {
	case TypeData:
		n, err := copyPayload(w, r, length)
		if err != nil {
			return channel, n, fmt.Errorf("copying data for channel %d failed: %w", channel, err)
		}
		return channel, n, err
	case TypeClose:
		return p.readClose(r, channel, length)
	}

	return channel, 0, fmt.Errorf("unknown payload type: %d", _type)
}

// read next headers
func (p packer) Next(r io.Reader) (Channel, int64, error) {
	_type, channel, length, err := p.readHeader(r)
	if err != nil {
		return 0, 0, err
	}

	if _type == TypeClose {
		return p.readClose(r, channel, length)
	}

	return channel, length, nil
}
