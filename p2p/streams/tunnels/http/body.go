package httptun

import (
	"io"

	"github.com/taubyte/tau/p2p/streams/packer"
)

type bodyReader struct {
	packer packer.Packer
	ch     packer.Channel
	pre    io.Reader
	err    error
	stream io.Reader
	len    int
}

func newBodyReader(p packer.Packer, ch packer.Channel, strm io.Reader) io.ReadCloser {
	return &bodyReader{
		packer: p,
		ch:     ch,
		stream: strm,
	}
}

func (b *bodyReader) Close() error {
	//TODO: send close to frontend
	return nil
}

func (b *bodyReader) Read(p []byte) (n int, err error) {
	defer func() {
		b.len += n
	}()

	if b.err != nil {
		n, err = b.pre.Read(p)
		if err != nil { // only EOF is possible here
			err = b.err
		}
		return
	}

	if b.pre != nil {
		n, err = b.pre.Read(p)
		if n > 0 || err == nil {
			return
		}
	}

	var (
		ch packer.Channel
		l  int64
	)

	ch, l, err = b.packer.Next(b.stream)
	if err != nil {
		b.err = err
		return
	}

	if ch != b.ch {
		var p [512]byte
		r := io.LimitReader(b.stream, l)
		for {
			_, _err := r.Read(p[:])
			if _err != nil {
				break
			}
		}
		return 0, ErrNotBody
	}

	b.pre = io.LimitReader(b.stream, l)
	n, err = b.pre.Read(p)

	return
}
