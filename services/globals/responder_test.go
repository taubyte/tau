package globals

import (
	"context"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/require"

	pkgcache "github.com/taubyte/tau/pkg/cache"
	"github.com/taubyte/tau/pkg/certificate/acme"
)

// TestAnAnswerIsConfirmedOnlyOnceEveryResponderHoldsIt: a dns node or gateway
// with a copy of the store that has not caught up answers false, and the
// issuer's confirmation waits for it.
func TestAnAnswerIsConfirmedOnlyOnceEveryResponderHoldsIt(t *testing.T) {
	f := newFleet(t, 1, false)
	s := f.nodes[0]

	// A RESPONDER ON A HOST OF ITS OWN, over a copy of the store that has not
	// seen what s wrote.
	behind, err := ChallengesIn(t.Context(), pkgcache.NewMemStore(), time.Now)
	require.NoError(t, err)
	rh := newHost(t, f.mn)
	r, err := ServeResponder(t.Context(), rh, behind, nil)
	require.NoError(t, err)
	t.Cleanup(func() { r.Close() })
	f.connect(t)

	var responders []peer.ID
	s.responders = func(string) []peer.ID { return responders }
	_, err = s.Confirmed(t.Context(), acme.KindDNS, "a."+zone, "v")
	require.ErrorContains(t, err, "no node answers")

	responders = []peer.ID{s.host.ID(), rh.ID()}
	for _, kind := range []string{acme.KindDNS, acme.KindHTTP} {
		name, value := "a."+zone, "v-"+kind
		if kind == acme.KindHTTP {
			require.NoError(t, s.challenges.PresentHTTP(t.Context(), "tok", value))
			name = "tok"
		} else {
			require.NoError(t, s.challenges.Present(t.Context(), name, value))
		}
		ok, err := s.Confirmed(t.Context(), kind, name, value)
		require.NoError(t, err)
		require.False(t, ok, "%s: the responder behind does not hold it", kind)

		if kind == acme.KindHTTP {
			require.NoError(t, behind.PresentHTTP(t.Context(), "tok", value))
		} else {
			require.NoError(t, behind.Present(t.Context(), name, value))
		}
		ok, err = s.Confirmed(t.Context(), kind, name, value)
		require.NoError(t, err)
		require.True(t, ok, kind)
	}

	// THE GATEWAY'S READ is the store's, and withdrawing one answer leaves
	// another order's for the same token.
	got, ok := behind.Challenge("tok")
	require.True(t, ok)
	require.Equal(t, "v-"+acme.KindHTTP, got)
	require.NoError(t, behind.PresentHTTP(t.Context(), "tok", "other"))
	require.NoError(t, behind.CleanUpHTTP(t.Context(), "tok", "v-"+acme.KindHTTP))
	got, ok = behind.Challenge("tok")
	require.True(t, ok)
	require.Equal(t, "other", got)
	_, ok = behind.Challenge("absent")
	require.False(t, ok)

	// A KIND THAT IS NEITHER is refused, over the wire too.
	_, err = behind.Values(t.Context(), "tls-alpn-01", "x")
	require.Error(t, err)
	_, err = s.Confirmed(t.Context(), "tls-alpn-01", "x", "v")
	require.Error(t, err)

	// A RESPONDER THAT CANNOT BE REACHED is not one that holds it.
	responders = []peer.ID{s.host.ID(), peer.ID("nobody")}
	ok, err = s.Confirmed(t.Context(), acme.KindDNS, "a."+zone, "v-"+acme.KindDNS)
	require.Error(t, err)
	require.False(t, ok)

	_, err = ServeResponder(t.Context(), nil, behind, nil)
	require.ErrorIs(t, err, ErrNoHost)
	_, err = ServeResponder(t.Context(), rh, nil, nil)
	require.ErrorIs(t, err, ErrNoChallenges)
}

// TestAClientWithAMemberSilentIsNotAnEmptyAnswer: an answer the silent member
// might hold makes nothing found an error, which a dns node turns into
// SERVFAIL rather than "no record".
func TestAClientWithAMemberSilentIsNotAnEmptyAnswer(t *testing.T) {
	f := newFleet(t, 1, false)
	c, err := NewClient(newHost(t, f.mn), func() []peer.ID {
		return append(f.members(), peer.ID("nobody"))
	}, func() *Service { return nil }, f.vault, zone)
	require.NoError(t, err)
	t.Cleanup(func() { c.Close() })
	f.connect(t)

	_, err = c.Challenges(t.Context(), "a."+zone)
	require.Error(t, err)
	_, ok := c.Challenge("tok")
	require.False(t, ok)

	require.NoError(t, f.nodes[0].challenges.Present(t.Context(), "a."+zone, "v"))
	values, err := c.Challenges(t.Context(), "a."+zone)
	require.NoError(t, err)
	require.Equal(t, []string{"v"}, values)
	require.NoError(t, f.nodes[0].challenges.PresentHTTP(t.Context(), "tok", "ka"))
	got, ok := c.Challenge("tok")
	require.True(t, ok)
	require.Equal(t, "ka", got)

	_, err = f.nodes[0].Challenges(context.Background(), acme.KindHTTP, "tok")
	require.NoError(t, err)
}

// TestAResponderOnlyAnotherEdgeNodeKnowsIsConfirmedToo: the placing node's
// view is behind a dns node's, which hands the CA a gateway the placing node
// has not heard of. That gateway is asked too.
func TestAResponderOnlyAnotherEdgeNodeKnowsIsConfirmedToo(t *testing.T) {
	f := newFleet(t, 1, false)
	s := f.nodes[0]
	serve := func(view func(string) []peer.ID) (*Challenges, peer.ID) {
		c, err := ChallengesIn(t.Context(), pkgcache.NewMemStore(), time.Now)
		require.NoError(t, err)
		h := newHost(t, f.mn)
		r, err := ServeResponder(t.Context(), h, c, view)
		require.NoError(t, err)
		t.Cleanup(func() { r.Close() })
		return c, h.ID()
	}
	unknown, unknownID := serve(nil)
	var edgeID peer.ID
	edge, id := serve(func(kind string) []peer.ID {
		if kind == acme.KindHTTP {
			return []peer.ID{edgeID, unknownID}
		}
		return nil
	})
	edgeID = id
	f.connect(t)
	s.responders = func(string) []peer.ID { return []peer.ID{edgeID} }
	require.NoError(t, edge.PresentHTTP(t.Context(), "tok", "ka"))
	ok, err := s.Confirmed(t.Context(), acme.KindHTTP, "tok", "ka")
	require.NoError(t, err)
	require.False(t, ok, "the gateway only the edge node knows does not hold it")
	require.NoError(t, unknown.PresentHTTP(t.Context(), "tok", "ka"))
	ok, err = s.Confirmed(t.Context(), acme.KindHTTP, "tok", "ka")
	require.NoError(t, err)
	require.True(t, ok)
}
