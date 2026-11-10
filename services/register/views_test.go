package register

import (
	"testing"
	"time"

	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/p2p/discovery"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/p2p/peer/peertest"
	"github.com/taubyte/tau/p2p/rpc"
	pkgregister "github.com/taubyte/tau/pkg/register"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

// node is an instance in a region, running what it is given.
type node struct {
	peer.Node
	*service.Served
	region  string
	finding finding
}

func (n *node) Peer() peer.Node          { return n.Node }
func (*node) Running() []service.Running { return nil }
func (*node) Config(any) error           { return nil }
func (*node) Root() string               { return "" }
func (*node) Cloud() string              { return "views-test" }
func (n *node) Region() string           { return n.region }
func (*node) Now() time.Time             { return time.Now() }

var _ service.Host = (*node)(nil)

// finding is a finder that finds the nodes it was told serve the register.
type finding []libp2ppeer.ID

func (f *finding) Find(protocol.ID) discovery.Resolver { return discovery.Static(*f...) }

func mockNode(t testing.TB) *node {
	t.Helper()
	p := peertest.Mock(t, t.Context())
	t.Cleanup(p.Close)
	n := &node{Node: p}
	n.Served = service.NewServed(p.ID(), &n.finding, rpc.Retry{For: 50 * time.Millisecond})
	return n
}

// startRegister runs the register on n, holding one deployment cut in two regions and
// whole for the cloud with none.
func startRegister(t *testing.T, n *node) Service {
	t.Helper()
	s := New()
	t.Cleanup(func() { s.Close() })
	require.NoError(t, s.Start(t.Context(), n))
	_, err := s.Local().Publish(t.Context(), pkgregister.Publication{
		Project: "shop", Environment: "prod", Revision: 1, Compiled: []byte("whole"), Hosts: []string{"shop.example.com"},
		Views: []pkgregister.View{
			{Destination: "region-1", Compiled: []byte("region-1")},
			{Destination: schema.DefaultRegion, Compiled: []byte("default")},
		},
	})
	require.NoError(t, err)
	return s
}

func TestViewsReadTheRegisterBesideThemInTheirRegion(t *testing.T) {
	ctx := t.Context()
	n := mockNode(t)
	s := startRegister(t, n)

	in := &node{Node: n.Node, Served: n.Served, region: "region-1"}
	served, ok := in.Local(Protocol)
	require.True(t, ok, "the register did not say its node serves it")
	require.Same(t, s, served)
	v := ViewsOn(in)
	t.Cleanup(func() { v.Close() })

	p, err := v.Resolve(ctx, "shop", "prod")
	require.NoError(t, err)
	require.Equal(t, pkgregister.IDOf([]byte("region-1")), p.Object, "the view placed in its region, not the whole")
	doc, err := v.Object(ctx, p.Object)
	require.NoError(t, err)
	require.Equal(t, "region-1", string(doc))

	project, environment, err := v.ResolveHost(ctx, "shop.example.com")
	require.NoError(t, err)
	require.Equal(t, []string{"shop", "prod"}, []string{project, environment})

	in.region = ""
	p, err = ViewsOn(in).Resolve(ctx, "shop", "prod")
	require.NoError(t, err)
	require.Equal(t, pkgregister.IDOf([]byte("default")), p.Object, "a cloud with no regions reads the default region's view")
}

func TestViewsReachARegisterElsewhere(t *testing.T) {
	ctx := t.Context()
	at, from := mockNode(t), mockNode(t)
	startRegister(t, at)
	require.NoError(t, peertest.LinkAll(t))
	require.NoError(t, from.Host().Connect(ctx, libp2ppeer.AddrInfo{ID: at.ID(), Addrs: at.Host().Addrs()}))

	from.region = "region-1"
	from.finding = finding{at.ID()}
	v := ViewsOn(from)

	p, err := v.Resolve(ctx, "shop", "prod")
	require.NoError(t, err)
	require.Equal(t, pkgregister.IDOf([]byte("region-1")), p.Object)
	project, _, err := v.ResolveHost(ctx, "shop.example.com")
	require.NoError(t, err)
	require.Equal(t, "shop", project)
	require.NoError(t, v.Close())
}

func TestViewsWithNoRegisterToReadAreRefusedByName(t *testing.T) {
	ctx := t.Context()
	v := ViewsOn(mockNode(t))
	t.Cleanup(func() { v.Close() })

	_, err := v.Resolve(ctx, "shop", "prod")
	require.ErrorIs(t, err, service.ErrNoneRuns)
	_, err = v.Object(ctx, pkgregister.IDOf([]byte("x")))
	require.ErrorIs(t, err, service.ErrNoneRuns)
	_, _, err = v.ResolveHost(ctx, "shop.example.com")
	require.ErrorIs(t, err, service.ErrNoneRuns)
}
