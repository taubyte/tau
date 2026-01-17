package readerutil

import "io"

// CountingReader wraps a Reader, incrementing N by the number of
// bytes read. No locking is performed.
type CountingReader struct {
	Reader io.Reader
	N      *int64
}

func (cr CountingReader) Read(p []byte) (n int, err error) {
	n, err = cr.Reader.Read(p)
	*cr.N += int64(n)
	return
}

// NewCountingReader returns a CountingReader that wraps r and counts bytes in n.
func NewCountingReader(r io.Reader, n *int64) *CountingReader {
	return &CountingReader{Reader: r, N: n}
}
