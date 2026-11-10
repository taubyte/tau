package auth

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/p2p/peer/peertest"
	"github.com/taubyte/tau/p2p/rpc"
	kvdbmock "github.com/taubyte/tau/pkg/kvdb/mock"
	"github.com/taubyte/tau/services/auth/projects"
)

// node is what auth starts on: a peer, the databases it shares, what runs beside
// it, and the config an operator wrote.
type node struct {
	peer.Node
	*service.Served
	running []service.Running
	config  func(into any) error
}

func (n *node) Peer() peer.Node            { return n.Node }
func (n *node) Running() []service.Running { return n.running }
func (*node) Root() string                 { return "" }
func (*node) Cloud() string                { return "auth-test" }
func (*node) Region() string               { return "" }
func (*node) Now() time.Time               { return time.Now() }
func (n *node) Config(into any) error {
	if n.config == nil {
		return nil
	}
	return n.config(into)
}

// shared is a node's databases.
type shared struct{ err error }

func (s shared) Database(name string) (kvdb.Instance, error) {
	if s.err != nil {
		return nil, s.err
	}
	return kvdbmock.New().New(nil, name, 0)
}

func newNode(t *testing.T, running ...any) *node {
	t.Helper()
	p := peertest.Mock(t, t.Context())
	t.Cleanup(p.Close)
	n := &node{Node: p, Served: service.NewServed(p.ID(), nil, rpc.Retry{For: 50 * time.Millisecond})}
	for _, r := range running {
		n.running = append(n.running, service.Running{Service: r})
	}
	return n
}

func TestAuthStartsFromItsNodeAndServesItsProtocolThere(t *testing.T) {
	n := newNode(t, shared{})
	s := New()
	t.Cleanup(func() { s.Close() })
	require.NoError(t, s.Start(t.Context(), n))

	served, ok := n.Local(Protocol)
	require.True(t, ok, "auth did not say its node serves its protocol")
	require.Same(t, s, served)
	require.Equal(t, "tau:auth-test", s.cfg.Issuer, "a node naming no issuer issues as its cloud")
	_, ok = s.identity.(*Users)
	require.True(t, ok, "with users kept, auth decides admission from them")
}

func TestTheProjectTableIsAuthsOwnBesideIt(t *testing.T) {
	require.Equal(t, Protocol, projects.Protocol, "the table is served on auth's listener")
	n := newNode(t, shared{})
	s := New()
	t.Cleanup(func() { s.Close() })
	require.NoError(t, s.Start(t.Context(), n))

	table := projects.On(n)
	t.Cleanup(func() { table.Close() })
	rec, err := table.LinkProject(t.Context(), projects.Link{Project: "QmShop", Config: "github.com/acme/shop", Claimant: "alice"})
	require.NoError(t, err)
	held, err := s.Projects().Project(t.Context(), "QmShop")
	require.NoError(t, err)
	require.Equal(t, rec, held, "the link landed in auth's own table, with no client between")
}

func TestAuthWithNoStoreKeepsNoTable(t *testing.T) {
	p := peertest.Mock(t, t.Context())
	t.Cleanup(p.Close)
	s, err := build(p.Host())
	require.NoError(t, err)
	require.Nil(t, s.Projects())
}

func TestAuthTakesTheIdentityItsNodeGivesIt(t *testing.T) {
	n := newNode(t, shared{}, AdmitAll{})
	s := New()
	t.Cleanup(func() { s.Close() })
	require.NoError(t, s.Start(t.Context(), n))
	require.Equal(t, AdmitAll{}, s.identity)
}

func TestAuthRefusesWhatItCannotStartWith(t *testing.T) {
	require.ErrorContains(t, New().Start(t.Context(), newNode(t)), "no databases")
	require.ErrorContains(t, New().Start(t.Context(), newNode(t, shared{err: errors.New("disk gone")})), "disk gone")

	broken := newNode(t, shared{})
	broken.config = func(any) error { return errors.New("no such key") }
	require.ErrorContains(t, New().Start(t.Context(), broken), "no such key")

	undriven := newNode(t, shared{})
	undriven.config = func(into any) error {
		into.(*hostConfig).Auth = Config{Forges: []ForgeApp{{Host: "code.example.com", ClientID: "c"}}}
		return nil
	}
	require.Error(t, New().Start(t.Context(), undriven), "a forge app for a forge the cloud does not serve")
}
