package livelog

import (
	"compress/lzw"
	"errors"
	"io"
)

// Encoding is how a finished log is compressed before it is stored: LZW, which
// streams both ways with no length known in advance.
const Encoding = "lzw"

// Compressed is content as LZW, streamed: the log is never held whole.
func Compressed(content io.Reader) *Compressing {
	r, w := io.Pipe()
	go compress(w, content)
	return &Compressing{pipe: r}
}

func compress(w *io.PipeWriter, content io.Reader) {
	lw := lzw.NewWriter(w, lzw.LSB, 8)
	_, err := io.Copy(lw, content)
	w.CloseWithError(errors.Join(err, lw.Close()))
}

// Compressing is the compressed log as a store reads it: how much it took, and
// whether it read to the end. A store that stops early stored a prefix.
type Compressing struct {
	pipe  *io.PipeReader
	n     int64
	ended bool
}

func (c *Compressing) Read(p []byte) (int, error) {
	n, err := c.pipe.Read(p)
	c.n += int64(n)
	if err == io.EOF {
		c.ended = true
	}
	return n, err
}

// Close ends the compression, which a store that stopped reading left writing.
func (c *Compressing) Close() error { return c.pipe.Close() }

// Ended is whether the stream was read to its end.
func (c *Compressing) Ended() bool { return c.ended }

// Taken is how many compressed bytes were read.
func (c *Compressing) Taken() int64 { return c.n }

// Decompressed is a stored log read back.
func Decompressed(stored io.ReadCloser) io.ReadCloser {
	return decompressing{ReadCloser: lzw.NewReader(stored, lzw.LSB, 8), stored: stored}
}

type decompressing struct {
	io.ReadCloser
	stored io.Closer
}

func (d decompressing) Close() error { return errors.Join(d.ReadCloser.Close(), d.stored.Close()) }
