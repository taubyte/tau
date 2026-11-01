package splice_test

import (
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/splice"
)

// pair is two ends of a real socket, not net.Pipe, which cannot half-close.
func pair(t testing.TB) (client, server net.Conn) {
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

func TestAPipeIsFullyClosed(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()

	splice.DoneWriting(a)

	// b reads EOF because a was closed outright — the only thing a pipe allows.
	b.SetReadDeadline(time.Now().Add(time.Second))
	_, err := b.Read(make([]byte, 1))
	require.Error(t, err)
}

// A shaper sees every byte, unlike Both, where the kernel moves them.

// upper is the simplest shaper that changes what arrives.
func upper(chunk []byte, last bool) ([]byte, error) {
	out := make([]byte, len(chunk))
	for i, b := range chunk {
		out[i] = byte(strings.ToUpper(string(b))[0])
	}
	return out, nil
}

func TestThroughShapesOneDirectionAndCopiesTheOther(t *testing.T) {
	clientA, serverA := pair(t)
	clientB, serverB := pair(t)

	go splice.Through(serverA, clientB, upper, nil)

	// The far end of B echoes what it is sent.
	go func() {
		body, _ := io.ReadAll(serverB)
		serverB.Write(append([]byte("saw:"), body...))
		splice.DoneWriting(serverB)
	}()

	clientA.SetDeadline(time.Now().Add(3 * time.Second))
	_, err := clientA.Write([]byte("hello"))
	require.NoError(t, err)
	require.NoError(t, clientA.(*net.TCPConn).CloseWrite())

	got, err := io.ReadAll(clientA)
	require.NoError(t, err)
	require.Equal(t, "saw:HELLO", string(got), "shaped on the way in, untouched on the way back")
}

func TestThroughCallsAShaperOnceMoreAtTheEnd(t *testing.T) {
	clientA, serverA := pair(t)
	clientB, serverB := pair(t)

	var held []byte
	hold := func(chunk []byte, last bool) ([]byte, error) {
		held = append(held, chunk...)
		if !last {
			return nil, nil
		}
		out := held
		held = nil
		return out, nil
	}

	go splice.Through(serverA, clientB, hold, nil)
	go func() {
		body, _ := io.ReadAll(serverB)
		serverB.Write(body)
		splice.DoneWriting(serverB)
	}()

	clientA.SetDeadline(time.Now().Add(3 * time.Second))
	for _, part := range []string{"one ", "two ", "three"} {
		_, err := clientA.Write([]byte(part))
		require.NoError(t, err)
		time.Sleep(10 * time.Millisecond)
	}
	require.NoError(t, clientA.(*net.TCPConn).CloseWrite())

	got, err := io.ReadAll(clientA)
	require.NoError(t, err)
	require.Equal(t, "one two three", string(got), "nothing moved until the end, and then all of it did")
}

func TestAShaperThatFailsEndsTheWholeConnection(t *testing.T) {
	clientA, serverA := pair(t)
	clientB, serverB := pair(t)

	boom := errors.New("not allowed")
	done := make(chan error, 1)
	go func() {
		done <- splice.Through(serverA, clientB, func([]byte, bool) ([]byte, error) { return nil, boom }, nil)
	}()

	clientA.SetDeadline(time.Now().Add(3 * time.Second))
	clientA.Write([]byte("hello"))

	select {
	case err := <-done:
		require.ErrorIs(t, err, boom)
	case <-time.After(3 * time.Second):
		t.Fatal("Through did not return")
	}

	// And the other end is done too, rather than waiting for bytes nobody will
	// send.
	serverB.SetDeadline(time.Now().Add(time.Second))
	_, err := io.ReadAll(serverB)
	require.NoError(t, err)
}

func TestThroughWithNoShapersIsAPlainCopy(t *testing.T) {
	clientA, serverA := pair(t)
	clientB, serverB := pair(t)

	go splice.Through(serverA, clientB, nil, nil)
	go func() {
		body, _ := io.ReadAll(serverB)
		serverB.Write(append([]byte("echo:"), body...))
		splice.DoneWriting(serverB)
	}()

	clientA.SetDeadline(time.Now().Add(3 * time.Second))
	clientA.Write([]byte("hello"))
	require.NoError(t, clientA.(*net.TCPConn).CloseWrite())

	got, err := io.ReadAll(clientA)
	require.NoError(t, err)
	require.Equal(t, "echo:hello", string(got))
}
