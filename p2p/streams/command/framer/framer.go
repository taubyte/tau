package framer

import (
	"bytes"
	"io"

	"github.com/fxamacker/cbor/v2"
	"github.com/taubyte/tau/p2p/streams/packer"
)

func Send(magic packer.Magic, version packer.Version, s io.Writer, obj interface{}) error {
	var buf bytes.Buffer
	enc := cbor.NewEncoder(&buf)

	err := enc.Encode(obj)
	if err != nil {
		return err
	}

	pack := packer.New(magic, version)

	err = pack.Send(0, s, &buf, int64(buf.Len()))
	if err != nil {
		return err
	}

	return err
}

func Read(magic packer.Magic, version packer.Version, s io.Reader, obj interface{}) error {
	pack := packer.New(magic, version)

	var buf bytes.Buffer
	_, _, err := pack.Recv(s, &buf)
	if err != nil {
		return err
	}

	return cbor.Unmarshal(buf.Bytes(), obj)
}
