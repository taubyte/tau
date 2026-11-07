package invoke

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// The two calls a proxy makes of the pool, both pooled, and both refusing a tier
// that does not serve their world rather than defaulting.

// steering is an instance that serves both proxy worlds, recording what it was
// handed so a test can prove the arguments crossed.
type steering struct {
	sawConn   *Connection
	sawPeeked []byte
	sawStream *Stream

	direct func() (*Direction, error)
	shape  func() (*Shaping, error)
}

// A proxy world's instance still has to be an Instance: the pool starts and
// closes it like any other, and only the side interface differs.
func (s *steering) Serve(context.Context, *Request) (*Response, error) {
	return &Response{Status: 200}, nil
}

func (s *steering) Close() error { return nil }

func (s *steering) Direct(_ context.Context, conn *Connection, peeked []byte) (*Direction, error) {
	s.sawConn, s.sawPeeked = conn, peeked
	return s.direct()
}

func (s *steering) Shape(_ context.Context, st *Stream) (*Shaping, error) {
	s.sawStream = st
	return s.shape()
}

// steers is a runtime whose one instance is the given steering.
type steers struct{ inst Instance }

func (s steers) Name() string { return "fake" }

func (s steers) Start(context.Context, Spec) (Instance, error) { return s.inst, nil }

func (s steers) Close() error { return nil }

func conn() *Connection {
	return &Connection{Source: "203.0.113.7:51000", Host: "db.example.com", Protocol: "postgres", Transport: "tcp"}
}

func TestThePoolAsksAProxyRouterWhereAConnectionGoes(t *testing.T) {
	inst := &steering{direct: func() (*Direction, error) {
		return &Direction{To: &Endpoint{Kind: "service", Name: "db", Port: "pg"}}, nil
	}}
	p, _ := newPool(t, steers{inst: inst}, nil)

	out, err := p.Direct(t.Context(), spec("shop/prod/function/usher", "r1"), conn(), []byte("pgwire"))
	require.NoError(t, err)
	require.Equal(t, "db", out.To.Name)
	require.Equal(t, "pg", out.To.Port)

	require.Equal(t, "db.example.com", inst.sawConn.Host)
	require.Equal(t, "pgwire", string(inst.sawPeeked))
}

func TestThePoolRunsAChunkThroughAProxyTransformer(t *testing.T) {
	inst := &steering{shape: func() (*Shaping, error) {
		return &Shaping{Pass: &Shaped{Out: []byte("OUT"), State: []byte("held")}}, nil
	}}
	p, _ := newPool(t, steers{inst: inst}, nil)

	out, err := p.Shape(t.Context(), spec("shop/prod/function/sieve", "r1"),
		&Stream{Conn: conn(), Way: Outbound, State: []byte("was"), Chunk: []byte("raw"), Last: true})
	require.NoError(t, err)
	require.Equal(t, "OUT", string(out.Pass.Out))
	require.Equal(t, "held", string(out.Pass.State))

	require.Equal(t, Outbound, inst.sawStream.Way)
	require.Equal(t, "was", string(inst.sawStream.State))
	require.Equal(t, "raw", string(inst.sawStream.Chunk))
	require.True(t, inst.sawStream.Last)
}

func TestATierThatRunsNeitherProxyWorldRefuses(t *testing.T) {
	p, _ := newPool(t, newFakeRuntime("fake"), nil)

	_, err := p.Direct(t.Context(), spec("shop/prod/function/usher", "r1"), conn(), nil)
	require.ErrorIs(t, err, ErrNoRuntime)
	require.ErrorContains(t, err, "no proxy router")

	_, err = p.Shape(t.Context(), spec("shop/prod/function/sieve", "r1"), &Stream{Conn: conn()})
	require.ErrorIs(t, err, ErrNoRuntime)
	require.ErrorContains(t, err, "no proxy transformer")
}

func TestThePoolPassesAProxyWorldsFailureBack(t *testing.T) {
	boom := errors.New("the component trapped")

	inst := &steering{
		direct: func() (*Direction, error) { return nil, boom },
		shape:  func() (*Shaping, error) { return nil, boom },
	}
	p, _ := newPool(t, steers{inst: inst}, nil)

	_, err := p.Direct(t.Context(), spec("shop/prod/function/usher", "r1"), conn(), nil)
	require.ErrorIs(t, err, boom)

	_, err = p.Shape(t.Context(), spec("shop/prod/function/sieve", "r1"), &Stream{Conn: conn()})
	require.ErrorIs(t, err, boom)
}

func TestEnoughIsWhatStopsAPeekLoopSpinning(t *testing.T) {
	require.True(t, (&Direction{More: 4}).Enough(4), "it has been shown what it asked for")
	require.True(t, (&Direction{More: 4}).Enough(9), "and more than it asked for")
	require.False(t, (&Direction{More: 4}).Enough(3))
	require.True(t, (&Direction{}).Enough(0), "a decision that wants nothing wants nothing more")
}
