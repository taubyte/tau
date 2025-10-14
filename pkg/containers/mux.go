package containers

import (
	"io"

	"github.com/docker/docker/pkg/stdcopy"
)

// Combined returns the Stderr, and Stdout combined container logs.
func (mx *MuxedReadCloser) Combined() io.ReadCloser {
	r, w := io.Pipe()
	go func() {
		defer w.Close()
		defer mx.reader.Close()
		stdcopy.StdCopy(w, w, mx.reader)
	}()
	return r
}

// Separated returns both the standard Out and Error logs of the container.
func (mx *MuxedReadCloser) Separated() (stdOut io.ReadCloser, stdErr io.ReadCloser) {
	r, w := io.Pipe()
	rE, wE := io.Pipe()
	go func() {
		defer w.Close()
		defer wE.Close()
		defer mx.reader.Close()
		stdcopy.StdCopy(w, wE, mx.reader)
	}()
	return r, rE
}

func (mx *MuxedReadCloser) Close() error {
	return mx.reader.Close()
}
