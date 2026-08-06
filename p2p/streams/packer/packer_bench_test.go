package packer

import (
	"bytes"
	"fmt"
	"io"
	"testing"
)

// netWriter models a libp2p stream: it implements io.Writer and nothing else.
// That matters — *bytes.Buffer implements io.ReaderFrom, so benchmarking
// against one takes a fast path the real transport never offers, and hides
// both the copy buffer and the per-field write count.
type netWriter struct {
	n      int64
	writes int
}

func (w *netWriter) Write(p []byte) (int, error) {
	w.n += int64(len(p))
	w.writes++
	return len(p), nil
}

func (w *netWriter) reset() { w.n, w.writes = 0, 0 }

// netReader is the same idea on the read side: no WriteTo to shortcut through.
type netReader struct {
	data []byte
	off  int
	// reads counts Read calls, which is what the header parsing costs in
	// round trips on a real stream.
	reads int
}

func (r *netReader) Read(p []byte) (int, error) {
	if r.off >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.off:])
	r.off += n
	r.reads++
	return n, nil
}

func (r *netReader) reset() { r.off, r.reads = 0, 0 }

var benchSizes = []int{64, 4 << 10, 64 << 10}

func BenchmarkSend(b *testing.B) {
	for _, size := range benchSizes {
		b.Run(fmt.Sprintf("size=%d", size), func(b *testing.B) {
			p := New(testMagic, testVersion)
			payload := make([]byte, size)
			src := &netReader{data: payload}
			dst := &netWriter{}

			b.ReportAllocs()
			b.SetBytes(int64(size))
			for b.Loop() {
				src.reset()
				dst.reset()
				if err := p.Send(1, dst, src, int64(size)); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportMetric(float64(dst.writes), "writes/op")
		})
	}
}

func BenchmarkRecv(b *testing.B) {
	for _, size := range benchSizes {
		b.Run(fmt.Sprintf("size=%d", size), func(b *testing.B) {
			p := New(testMagic, testVersion)
			frameBytes := frame(TypeData, int64(size), make([]byte, size))
			src := &netReader{data: frameBytes}
			dst := &netWriter{}

			b.ReportAllocs()
			b.SetBytes(int64(size))
			for b.Loop() {
				src.reset()
				dst.reset()
				if _, _, err := p.Recv(src, dst); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportMetric(float64(src.reads), "reads/op")
		})
	}
}

// BenchmarkRecvIntoBuffer is Recv as this repo actually calls it. Both real
// callers (command/framer and tunnels/http) receive into a fresh
// *bytes.Buffer, which implements io.ReaderFrom — so the copy runs through
// bytes.Buffer.ReadFrom and the packer's own buffer never enters it. The gain
// here is header parsing only; BenchmarkRecv's payload-buffer saving is real
// but applies to a destination no caller in this tree passes.
func BenchmarkRecvIntoBuffer(b *testing.B) {
	for _, size := range benchSizes {
		b.Run(fmt.Sprintf("size=%d", size), func(b *testing.B) {
			p := New(testMagic, testVersion)
			frameBytes := frame(TypeData, int64(size), make([]byte, size))
			src := &netReader{data: frameBytes}

			b.ReportAllocs()
			b.SetBytes(int64(size))
			for b.Loop() {
				src.reset()
				// Fresh buffer per call, exactly as framer.Read does.
				var out bytes.Buffer
				if _, _, err := p.Recv(src, &out); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkNext isolates header parsing — the path every frame pays, and the
// one the command router hits per request.
func BenchmarkNext(b *testing.B) {
	p := New(testMagic, testVersion)
	frameBytes := frame(TypeData, 0, nil)
	src := &netReader{data: frameBytes}

	b.ReportAllocs()
	for b.Loop() {
		src.reset()
		if _, _, err := p.Next(src); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(float64(src.reads), "reads/op")
}

func BenchmarkStream(b *testing.B) {
	for _, size := range benchSizes {
		b.Run(fmt.Sprintf("size=%d", size), func(b *testing.B) {
			p := New(testMagic, testVersion)
			payload := make([]byte, size)
			src := &netReader{data: payload}
			dst := &netWriter{}

			b.ReportAllocs()
			b.SetBytes(int64(size))
			for b.Loop() {
				src.reset()
				dst.reset()
				if _, err := p.Stream(1, dst, src, DefaultBufferSize); err != io.EOF {
					b.Fatal(err)
				}
			}
			b.ReportMetric(float64(dst.writes), "writes/op")
		})
	}
}
