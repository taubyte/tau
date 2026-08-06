package containers

import (
	"io"

	"github.com/moby/moby/api/pkg/stdcopy"
)

// Combined returns the Stderr, and Stdout combined container logs.
func (mx *MuxedReadCloser) Combined() io.ReadCloser {
	r, w := io.Pipe()
	go func() {
		defer mx.reader.Close()
		// A demultiplexing failure means the output is not what it claims to
		// be; surfacing it beats handing the caller a silently short read.
		w.CloseWithError(demux(w, w, mx.reader))
	}()
	return r
}

func demux(stdOut, stdErr io.Writer, muxed io.Reader) error {
	_, err := stdcopy.StdCopy(stdOut, stdErr, muxed)
	return err
}

func (mx *MuxedReadCloser) Close() error {
	return mx.reader.Close()
}
