package splice_test

import (
	"io"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/splice"
)

// pair is two ends of a real socket. NOT net.Pipe: a pipe cannot half-close,
// which is the whole property this package is about.
func pair(t *testing.T) (client, server net.Conn) {
	t.Helper()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer l.Close()

	done := make(chan net.Conn, 1)
	go func() {
		c, err := l.Accept()
		if err != nil {
			close(done)
			return
		}
		done <- c
	}()

	client, err = net.Dial("tcp", l.Addr().String())
	require.NoError(t, err)
	server = <-done
	require.NotNil(t, server)

	t.Cleanup(func() { client.Close(); server.Close() })
	return client, server
}

// TestAQUERYSurvivesTheSplice: send, finish sending, wait to be answered. It is
// the shape psql and redis and an HTTP request on a raw socket all have, and the
// one a splice that ends on the first EOF destroys — the request is delivered
// and the answer never comes, with no error anywhere.
func TestAHalfCloseDoesNotEndTheConnection(t *testing.T) {
	clientSide, edge := pair(t)
	workload, far := pair(t)

	go func() {
		body, _ := io.ReadAll(far)
		far.Write([]byte("answered:" + string(body)))
		far.(*net.TCPConn).CloseWrite()
	}()

	go splice.Both(edge, workload)

	_, err := clientSide.Write([]byte("a query"))
	require.NoError(t, err)
	require.NoError(t, clientSide.(*net.TCPConn).CloseWrite())

	clientSide.SetReadDeadline(time.Now().Add(2 * time.Second))
	got, err := io.ReadAll(clientSide)
	require.NoError(t, err)
	require.Equal(t, "answered:a query", string(got))
}

// TestBYTESGoBothWaysAtOnce, which a request/response test alone does not show:
// a protocol that streams in both directions at the same time is the case a
// half-duplex implementation would still pass.
func TestBothDirectionsCarryAtOnce(t *testing.T) {
	clientSide, edge := pair(t)
	workload, far := pair(t)

	go func() {
		buf := make([]byte, 4)
		for {
			n, err := far.Read(buf)
			if n > 0 {
				far.Write([]byte("<" + string(buf[:n]) + ">"))
			}
			if err != nil {
				return
			}
		}
	}()

	go splice.Both(edge, workload)

	for _, send := range []string{"one", "two"} {
		_, err := clientSide.Write([]byte(send))
		require.NoError(t, err)

		clientSide.SetReadDeadline(time.Now().Add(2 * time.Second))
		buf := make([]byte, 16)
		n, err := clientSide.Read(buf)
		require.NoError(t, err)
		require.Equal(t, "<"+send+">", string(buf[:n]), "still open after the last exchange")
	}
}

// TestACONNECTIONThatCannotHalfCloseIsClosed, rather than left waiting for an
// EOF nobody can send.
func TestAPipeIsFullyClosed(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()

	splice.DoneWriting(a)

	// b reads EOF because a was closed outright — the only thing a pipe allows.
	b.SetReadDeadline(time.Now().Add(time.Second))
	_, err := b.Read(make([]byte, 1))
	require.Error(t, err)
}
