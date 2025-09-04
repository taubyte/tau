package readerutil

import (
	"io"
)

// SizeReaderAt is a ReaderAt with a Size method.
// An io.SectionReader implements SizeReaderAt.
type SizeReaderAt interface {
	Size() int64
	io.ReaderAt
}

// ReadSeekCloser can Read, Seek, and Close.
type ReadSeekCloser interface {
	io.Reader
	io.Seeker
	io.Closer
}

// ReaderAtCloser can ReadAt and Close.
type ReaderAtCloser interface {
	io.ReaderAt
	io.Closer
}
