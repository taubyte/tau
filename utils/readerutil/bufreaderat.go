package readerutil

import "io"

// NewBufferingReaderAt returns an io.ReaderAt that reads from r as
// necessary and keeps a copy of all data read in memory.
func NewBufferingReaderAt(r io.Reader) io.ReaderAt {
	return &bufReaderAt{r: r}
}

type bufReaderAt struct {
	r   io.Reader
	buf []byte
}

func (br *bufReaderAt) ReadAt(p []byte, off int64) (n int, err error) {
	endOff := off + int64(len(p))
	need := endOff - int64(len(br.buf))
	if need > 0 {
		buf := make([]byte, need)
		var rn int
		rn, err = io.ReadFull(br.r, buf)
		br.buf = append(br.buf, buf[:rn]...)
	}
	if int64(len(br.buf)) >= off {
		n = copy(p, br.buf[off:])
	}
	if n == len(p) {
		err = nil
	}
	return
}
