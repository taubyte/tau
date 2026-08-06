package packer

import (
	"bytes"
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

func (p packer) send(channel Channel, _type Type, w io.Writer, r io.Reader, length int64) error {
	_, err := w.Write(p.magic[:])
	if err != nil {
		return fmt.Errorf("writing magic bytes failed: %w", err)
	}

	err = binary.Write(w, binary.LittleEndian, p.version)
	if err != nil {
		return fmt.Errorf("writing version failed: %w", err)
	}

	err = binary.Write(w, binary.LittleEndian, _type)
	if err != nil {
		return fmt.Errorf("writing type failed: %w", err)
	}

	err = binary.Write(w, binary.LittleEndian, length)
	if err != nil {
		return fmt.Errorf("writing length failed: %w", err)
	}

	err = binary.Write(w, binary.LittleEndian, channel)
	if err != nil {
		return fmt.Errorf("writing channel failed: %w", err)
	}

	lr := io.LimitReader(r, length)

	n, err := io.Copy(w, lr)
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
			err := p.Send(channel, w, bytes.NewBuffer(buf[:n]), int64(n))
			if err != nil {
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
	var buf bytes.Buffer
	if err != nil && err != io.EOF {
		buf.WriteString(err.Error())
	}

	return p.send(channel, TypeClose, w, &buf, int64(buf.Len()))
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
	var _magic [2]byte
	if _, err = io.ReadFull(r, _magic[:]); err != nil {
		return 0, 0, 0, fmt.Errorf("reading magic bytes failed: %w", err)
	}

	if _magic[0] != p.magic[0] || _magic[1] != p.magic[1] {
		return 0, 0, 0, fmt.Errorf("wrong packer magic: expected [%d %d], got [%d %d]", p.magic[0], p.magic[1], _magic[0], _magic[1])
	}

	var version Version
	if err = binary.Read(r, binary.LittleEndian, &version); err != nil {
		return 0, 0, 0, fmt.Errorf("reading version failed: %w", err)
	}

	if version != p.version {
		return 0, 0, 0, fmt.Errorf("wrong packer version: expected %d, got %d", p.version, version)
	}

	if err = binary.Read(r, binary.LittleEndian, &_type); err != nil {
		return 0, 0, 0, fmt.Errorf("reading type failed: %w", err)
	}

	if err = binary.Read(r, binary.LittleEndian, &length); err != nil {
		return 0, 0, 0, fmt.Errorf("reading length failed: %w", err)
	}

	if length < 0 {
		return 0, 0, 0, fmt.Errorf("negative payload length: %d", length)
	}

	if err = binary.Read(r, binary.LittleEndian, &channel); err != nil {
		return 0, 0, 0, fmt.Errorf("reading channel failed: %w", err)
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
		lr := io.LimitReader(r, length)
		n, err := io.Copy(w, lr)
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
