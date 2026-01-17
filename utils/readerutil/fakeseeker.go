package readerutil

import (
	"errors"
	"fmt"
	"io"
)

// fakeSeeker can seek to the ends but any read not at the current
// position will fail.
type fakeSeeker struct {
	r    io.Reader
	size int64

	fakePos int64
	realPos int64
}

// NewFakeSeeker returns a ReadSeeker that can pretend to Seek (based
// on the provided total size of the reader's content), but any reads
// will fail if the fake seek position doesn't match reality.
func NewFakeSeeker(r io.Reader, size int64) io.ReadSeeker {
	return &fakeSeeker{r: r, size: size}
}

func (fs *fakeSeeker) Seek(offset int64, whence int) (int64, error) {
	var newo int64
	switch whence {
	default:
		return 0, errors.New("invalid whence")
	case io.SeekStart:
		newo = offset
	case io.SeekCurrent:
		newo = fs.fakePos + offset
	case io.SeekEnd:
		newo = fs.size + offset
	}
	if newo < 0 {
		return 0, errors.New("negative seek")
	}
	fs.fakePos = newo
	return newo, nil
}

func (fs *fakeSeeker) Read(p []byte) (n int, err error) {
	if fs.fakePos != fs.realPos {
		return 0, fmt.Errorf("attempt to read from fake seek offset %d; real offset is %d", fs.fakePos, fs.realPos)
	}
	n, err = fs.r.Read(p)
	fs.fakePos += int64(n)
	fs.realPos += int64(n)
	return
}
