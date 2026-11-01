package splice_test

import (
	"io"
	"testing"

	"github.com/taubyte/tau/pkg/splice"
)

func BenchmarkThroughConnection(b *testing.B) {
	pass := func(chunk []byte, _ bool) ([]byte, error) { return chunk, nil }
	payload := make([]byte, 4096)
	b.ReportAllocs()
	for b.Loop() {
		clientA, serverA := pair(b)
		clientB, serverB := pair(b)
		done := make(chan error, 1)
		go func() { done <- splice.Through(serverA, clientB, pass, pass) }()
		go func() {
			io.Copy(io.Discard, serverB)
			serverB.Close()
		}()
		clientA.Write(payload)
		splice.DoneWriting(clientA)
		io.Copy(io.Discard, clientA)
		if err := <-done; err != nil {
			b.Fatal(err)
		}
		clientA.Close()
		serverA.Close()
		clientB.Close()
	}
}
