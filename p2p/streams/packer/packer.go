package packer

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

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
		return err
	}

	err = binary.Write(w, binary.LittleEndian, p.version)
	if err != nil {
		return err
	}

	err = binary.Write(w, binary.LittleEndian, _type)
	if err != nil {
		return err
	}

	err = binary.Write(w, binary.LittleEndian, length)
	if err != nil {
		return err
	}

	err = binary.Write(w, binary.LittleEndian, channel)
	if err != nil {
		return err
	}

	lr := io.LimitReader(r, length)

	n, err := io.Copy(w, lr)
	if n != length {
		return io.ErrShortWrite
	}

	if err != nil {
		return err
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

	buf := make([]byte, bufSize)

	for {
		n, err = r.Read(buf)
		l += int64(n)
		if n > 0 {
			err := p.Send(channel, w, bytes.NewBuffer(buf), int64(n))
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

func (p packer) Recv(r io.Reader, w io.Writer) (Channel, int64, error) {
	_magic := make([]byte, 2)
	_, err := r.Read(_magic)
	if err != nil {
		return 0, 0, err
	}

	if _magic[0] != p.magic[0] || _magic[1] != p.magic[1] {
		return 0, 0, errors.New("wrong packer magic")
	}

	var version Version
	err = binary.Read(r, binary.LittleEndian, &version)
	if err != nil {
		return 0, 0, err
	}

	if version != p.version {
		return 0, 0, errors.New("wrong packer version")
	}

	var _type Type
	err = binary.Read(r, binary.LittleEndian, &_type)
	if err != nil {
		return 0, 0, err
	}

	var length int64
	err = binary.Read(r, binary.LittleEndian, &length)
	if err != nil {
		return 0, 0, err
	}

	var channel Channel
	err = binary.Read(r, binary.LittleEndian, &channel)
	if err != nil {
		return 0, 0, err
	}

	switch _type {
	case TypeData:
		lr := io.LimitReader(r, length)
		n, err := io.Copy(w, lr)
		return channel, n, err
	case TypeClose:
		if length == 0 {
			return channel, 0, io.EOF
		}
		errMsg := make([]byte, length)
		io.ReadFull(r, errMsg)
		return channel, 0, errors.New(string(errMsg))
	}

	return channel, 0, errors.New("unknown payload type")
}

// read next headers
func (p packer) Next(r io.Reader) (Channel, int64, error) {
	_magic := make([]byte, 2)
	_, err := r.Read(_magic)
	if err != nil {
		return 0, 0, err
	}

	if _magic[0] != p.magic[0] || _magic[1] != p.magic[1] {
		return 0, 0, errors.New("wrong packer magic")
	}

	var version Version
	err = binary.Read(r, binary.LittleEndian, &version)
	if err != nil {
		return 0, 0, err
	}

	if version != p.version {
		return 0, 0, errors.New("wrong packer version")
	}

	var _type Type
	err = binary.Read(r, binary.LittleEndian, &_type)
	if err != nil {
		return 0, 0, err
	}

	var length int64
	err = binary.Read(r, binary.LittleEndian, &length)
	if err != nil {
		return 0, 0, err
	}

	var channel Channel
	err = binary.Read(r, binary.LittleEndian, &channel)
	if err != nil {
		return 0, 0, err
	}

	if _type == TypeClose {
		if length == 0 {
			return channel, 0, io.EOF
		}
		errMsg := make([]byte, length)
		io.ReadFull(r, errMsg)
		return channel, 0, errors.New(string(errMsg))
	}

	return channel, length, nil
}
