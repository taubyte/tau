package rpc

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Guards on things gostream and libp2p do not do today: a non-stream conn, a conn
// refusing a deadline, a listener dying for another reason. They must not crash.

type fakeListener struct {
	conn net.Conn
	err  error
	once bool
}

func (l *fakeListener) Accept() (net.Conn, error) {
	if l.err != nil {
		return nil, l.err
	}
	if l.once {
		return nil, errors.New("no more")
	}
	l.once = true
	return l.conn, nil
}
func (l *fakeListener) Close() error   { return nil }
func (l *fakeListener) Addr() net.Addr { return dummyAddr{} }

type dummyAddr struct{}

func (dummyAddr) Network() string { return "fake" }
func (dummyAddr) String() string  { return "fake" }

// plainConn is a net.Conn that is not a libp2p stream.
type plainConn struct {
	net.Conn
	deadlineErr error
	reads       [][]byte
}

func (c *plainConn) SetReadDeadline(time.Time) error { return c.deadlineErr }
func (c *plainConn) Read(b []byte) (int, error) {
	if len(c.reads) == 0 {
		return 0, errors.New("drained")
	}
	n := copy(b, c.reads[0])
	c.reads = c.reads[1:]
	return n, nil
}

func TestAcceptWithoutALibp2pStream(t *testing.T) {
	inner := &plainConn{}
	l := &serverListener{Listener: &fakeListener{conn: inner}, prefaceTimeout: time.Second}

	c, err := l.Accept()
	require.NoError(t, err)

	sc := c.(*serverConn)
	assert.Equal(t, peer.ID(""), sc.RemotePeerID())
	// No identity means no identity, not the zero peer.
	assert.Equal(t, context.Background(), carryRemotePeer(context.Background(), sc))
}

func TestAcceptWhenTheConnRefusesADeadline(t *testing.T) {
	inner := &plainConn{deadlineErr: errors.New("deadlines are not supported")}
	l := &serverListener{Listener: &fakeListener{conn: inner}, prefaceTimeout: time.Second}

	c, err := l.Accept()
	require.NoError(t, err)
	assert.False(t, c.(*serverConn).deadlined)
}

func TestAcceptWithoutAPrefaceTimeout(t *testing.T) {
	inner := &plainConn{deadlineErr: errors.New("should not be called")}
	l := &serverListener{Listener: &fakeListener{conn: inner}, prefaceTimeout: 0}

	c, err := l.Accept()
	require.NoError(t, err)
	assert.False(t, c.(*serverConn).deadlined)
}

func TestAcceptPassesItsErrorUp(t *testing.T) {
	want := errors.New("listener is gone")
	l := &serverListener{Listener: &fakeListener{err: want}}

	_, err := l.Accept()
	assert.ErrorIs(t, err, want)
}

func TestTheDeadlineSurvivesEveryByteButTheLast(t *testing.T) {
	// A read that returns nothing, then the preface one byte at a time, then
	// one byte past it.
	reads := [][]byte{{}}
	for i := range len(clientPreface) {
		reads = append(reads, []byte{clientPreface[i]})
	}
	reads = append(reads, []byte{0})

	cleared := 0
	inner := &countingDeadlineConn{plainConn: plainConn{reads: reads}}
	inner.onDeadline = func(d time.Time) {
		if d.IsZero() {
			cleared++
		}
	}
	sc := &serverConn{Conn: inner, deadlined: true}

	b := make([]byte, 8)
	n, err := sc.Read(b)
	require.NoError(t, err)
	require.Zero(t, n)
	require.Zero(t, cleared, "a read that returned nothing counted as speaking")

	for i := range len(clientPreface) {
		n, err := sc.Read(b)
		require.NoError(t, err)
		require.Equal(t, 1, n)
		if i < len(clientPreface)-1 {
			require.Zero(t, cleared, "lifted after %d of %d preface bytes", i+1, len(clientPreface))
		}
	}
	assert.Equal(t, 1, cleared, "a whole preface arrived and the deadline is still on")

	_, err = sc.Read(b)
	require.NoError(t, err)
	assert.Equal(t, 1, cleared, "the deadline should be lifted once, not on every read")
}

func TestAPrefaceInOneReadLiftsIt(t *testing.T) {
	cleared := 0
	inner := &countingDeadlineConn{plainConn: plainConn{reads: [][]byte{[]byte(clientPreface)}}}
	inner.onDeadline = func(d time.Time) {
		if d.IsZero() {
			cleared++
		}
	}
	sc := &serverConn{Conn: inner, deadlined: true}

	n, err := sc.Read(make([]byte, len(clientPreface)))
	require.NoError(t, err)
	require.Equal(t, len(clientPreface), n)
	assert.Equal(t, 1, cleared)
}

func TestAConnWithNoDeadlineIsNeverTouched(t *testing.T) {
	inner := &countingDeadlineConn{plainConn: plainConn{reads: [][]byte{[]byte(clientPreface)}}}
	inner.onDeadline = func(time.Time) { t.Fatal("the deadline was cleared on a conn that never had one") }
	sc := &serverConn{Conn: inner}

	_, err := sc.Read(make([]byte, len(clientPreface)))
	require.NoError(t, err)
}

type countingDeadlineConn struct {
	plainConn
	onDeadline func(time.Time)
}

func (c *countingDeadlineConn) SetReadDeadline(d time.Time) error {
	c.onDeadline(d)
	return nil
}

func TestServeRemembersWhyItStopped(t *testing.T) {
	want := errors.New("the listener fell over")
	s := &Server{
		listener: &serverListener{Listener: &fakeListener{err: want}},
		http:     &http.Server{Handler: http.NewServeMux()},
		done:     make(chan struct{}),
	}

	go s.serve()
	select {
	case <-s.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not return")
	}
	assert.ErrorIs(t, s.Err(), want)
}

func TestWatchReturnsWhenTheServerStops(t *testing.T) {
	s := &Server{done: make(chan struct{})}

	returned := make(chan struct{})
	go func() {
		s.watch(context.Background())
		close(returned)
	}()

	close(s.done)
	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("watch outlived the server")
	}
}
