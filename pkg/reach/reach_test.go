package reach

import (
	"context"
	"errors"
	"testing"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/require"
)

var errNo = errors.New("the service says no")

// node is one client: what it answers, and whether it was closed.
type node struct {
	id     peer.ID
	err    error
	asked  int
	closed bool
}

func (n *node) ask() error { n.asked++; return n.err }

type closer struct{ *node }

func (c closer) Close() error { c.closed = true; return nil }

type bare struct{ *node }

func (b bare) Close() { b.closed = true }

func reachOf(self peer.ID, local *node, peers []*node) *Reach[*node] {
	return New(self,
		func() (*node, bool) { return local, local != nil },
		func() []peer.ID {
			out := make([]peer.ID, 0, len(peers))
			for _, n := range peers {
				out = append(out, n.id)
			}
			return out
		},
		func(id peer.ID) (*node, error) {
			for _, n := range peers {
				if n.id == id {
					if n.err == errDial {
						return nil, errDial
					}
					return n, nil
				}
			}
			return nil, errDial
		},
		func(err error) bool { return errors.Is(err, errNo) })
}

var errDial = errors.New("unreachable")

func TestTheServiceHereIsAskedAndNoOther(t *testing.T) {
	here := &node{}
	far := &node{id: "far"}
	r := reachOf("self", here, []*node{far})
	require.NoError(t, r.Do(t.Context(), (*node).ask))
	require.Equal(t, 1, here.asked)
	require.Zero(t, far.asked)
}

func TestEachNodeIsAskedUntilOneAnswers(t *testing.T) {
	self := &node{id: "self"}
	down := &node{id: "down", err: errors.New("connection refused")}
	undialable := &node{id: "undialable", err: errDial}
	up := &node{id: "up"}
	after := &node{id: "after"}
	r := reachOf("self", nil, []*node{self, down, undialable, up, after})

	require.NoError(t, r.Do(t.Context(), (*node).ask))
	require.Zero(t, self.asked, "a node never asks itself")
	require.Equal(t, 1, down.asked)
	require.Equal(t, 1, up.asked)
	require.Zero(t, after.asked)

	// THE CLIENT IS KEPT: a second ask dials nothing new.
	require.NoError(t, r.Do(t.Context(), (*node).ask))
	require.Equal(t, 2, up.asked)
}

func TestAnAnswerIsFinal(t *testing.T) {
	no := &node{id: "no", err: errNo}
	next := &node{id: "next"}
	r := reachOf("self", nil, []*node{no, next})
	require.ErrorIs(t, r.Do(t.Context(), (*node).ask), errNo)
	require.Zero(t, next.asked, "another node would say the same")

	consumed := errors.New("half the stream went")
	first := &node{id: "first"}
	r = reachOf("self", nil, []*node{first, next})
	err := r.Do(t.Context(), func(n *node) error { n.asked++; return Final(consumed) })
	require.ErrorIs(t, err, consumed)
	require.False(t, errors.As(err, new(final)), "the mark does not leave Do")
	require.Zero(t, next.asked)
	require.NoError(t, Final(nil))

	here := &node{}
	r = reachOf("self", here, nil)
	require.ErrorIs(t, r.Do(t.Context(), func(*node) error { return Final(consumed) }), consumed)
}

func TestNobodyToAskSaysSo(t *testing.T) {
	r := reachOf("self", nil, nil)
	require.ErrorIs(t, r.Do(t.Context(), (*node).ask), ErrNone)

	down := &node{id: "down", err: errors.New("refused")}
	r = reachOf("self", nil, []*node{down})
	err := r.Do(t.Context(), (*node).ask)
	require.ErrorContains(t, err, "no node answered")
	require.ErrorContains(t, err, "refused")

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	r = reachOf("self", nil, []*node{{id: "up"}})
	require.ErrorIs(t, r.Do(ctx, (*node).ask), context.Canceled)
}

func TestClosingClosesEveryClient(t *testing.T) {
	a, b, c := &node{id: "a"}, &node{id: "b"}, &node{id: "c"}
	clients := map[peer.ID]any{"a": closer{a}, "b": bare{b}, "c": c}
	r := New("self", nil,
		func() []peer.ID { return []peer.ID{"a", "b", "c"} },
		func(id peer.ID) (any, error) { return clients[id], nil },
		func(error) bool { return false })
	for range 3 {
		r.Do(t.Context(), func(any) error { return errors.New("next") })
	}
	require.NoError(t, r.Close())
	require.True(t, a.closed)
	require.True(t, b.closed)
	require.Error(t, r.Do(t.Context(), func(any) error { return nil }), "a closed reach dials nothing")
}
