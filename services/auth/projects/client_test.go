package projects_test

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
	authmock "github.com/taubyte/tau/services/auth/mock"
	"github.com/taubyte/tau/services/auth/projects"
)

// node is an instance serving auth's protocol with the mock a test gives it, and
// finding the peers its finding names.
type node struct {
	peer.Node
	*service.Served
	finding finding
}

func newNode(t *testing.T, auth ...*authmock.Auth) *node {
	t.Helper()
	p := peertest.Mock(t, t.Context())
	t.Cleanup(p.Close)
	n := &node{Node: p}
	n.Served = service.NewServed(p.ID(), &n.finding, rpc.Retry{For: 50 * time.Millisecond})
	for _, a := range auth {
		n.Serve(projects.Protocol, a)
	}
	return n
}

func (n *node) Peer() peer.Node          { return n.Node }
func (*node) Running() []service.Running { return nil }
func (*node) Config(any) error           { return nil }
func (*node) Root() string               { return "" }
func (*node) Cloud() string              { return "projects-test" }
func (*node) Region() string             { return "" }
func (*node) Now() time.Time             { return time.Now() }

var _ service.Host = (*node)(nil)

// finding is discovery naming the peers it is given.
type finding []libp2ppeer.ID

func (f *finding) Find(protocol.ID) discovery.Resolver { return discovery.Static(*f...) }

const (
	shop   = "QmShop"
	config = "github.com/acme/shop"
	alice  = "github.com/1001"
)

func TestAuthOnTheSameNodeIsReachedWithNoClient(t *testing.T) {
	auth := authmock.New()
	c := projects.On(newNode(t, auth))
	t.Cleanup(func() { c.Close() })

	rec, err := c.LinkProject(t.Context(), projects.Link{Project: shop, Config: config, Claimant: alice})
	require.NoError(t, err)
	held, err := auth.Projects().Project(t.Context(), shop)
	require.NoError(t, err)
	require.Equal(t, rec, held, "the link landed in the table beside it, with no stream")
}

// remote is a client on one node of the table auth keeps on another.
func remote(t *testing.T) *projects.Client {
	t.Helper()
	ctx := t.Context()
	auth := authmock.New()
	far := newNode(t, auth)
	require.NoError(t, auth.Start(ctx, far))
	t.Cleanup(func() { auth.Close() })

	near := newNode(t)
	near.finding = finding{far.ID()}
	require.NoError(t, peertest.LinkAll(t))
	require.NoError(t, near.Host().Connect(ctx, libp2ppeer.AddrInfo{ID: far.ID(), Addrs: far.Host().Addrs()}))

	c := projects.On(near)
	t.Cleanup(func() { c.Close() })
	return c
}

func TestAuthOnAnotherNodeIsReachedOverItsProtocol(t *testing.T) {
	ctx := t.Context()
	c := remote(t)

	rec, err := c.LinkProject(ctx, projects.Link{Project: shop, Config: config, Code: []string{"github.com/acme/web"}, Claimant: alice})
	require.NoError(t, err)
	require.Equal(t, []string{config, "github.com/acme/web"}, rec.Repositories())

	got, err := c.Project(ctx, shop)
	require.NoError(t, err)
	require.Equal(t, rec, got)
	repo, err := c.Repository(ctx, "github.com/acme/web")
	require.NoError(t, err)
	require.Equal(t, projects.RepositoryRecord{Project: shop, Repository: "github.com/acme/web"}, repo)
	mine, err := c.ProjectsOf(ctx, alice)
	require.NoError(t, err)
	require.Equal(t, []projects.ProjectRecord{rec}, mine)
	all, err := c.ListProjects(ctx)
	require.NoError(t, err)
	require.Equal(t, []projects.ProjectRecord{rec}, all)

	g, err := c.GrantRole(ctx, shop, "bob", projects.RoleBuilder, alice)
	require.NoError(t, err)
	grants, err := c.Grants(ctx, shop)
	require.NoError(t, err)
	require.Equal(t, []projects.Grant{g}, grants)
	role, err := c.RoleOf(ctx, shop, "bob")
	require.NoError(t, err)
	require.Equal(t, projects.RoleBuilder, role)
	require.NoError(t, c.Revoke(ctx, shop, g.ID, alice))
	role, err = c.RoleOf(ctx, shop, "bob")
	require.NoError(t, err)
	require.Equal(t, projects.RoleNone, role)
}

func TestARefusalCrossesTheWireAsItsSentinel(t *testing.T) {
	ctx := t.Context()
	c := remote(t)
	_, err := c.LinkProject(ctx, projects.Link{Project: shop, Config: config, Claimant: alice})
	require.NoError(t, err)

	_, err = c.LinkProject(ctx, projects.Link{Project: shop, Config: "github.com/other/config", Claimant: "github.com/2"})
	require.ErrorIs(t, err, projects.ErrProjectClaimed)
	require.ErrorContains(t, err, "already claimed by "+config, "the owner is named")
	_, err = c.LinkProject(ctx, projects.Link{Project: "QmOther", Config: config, Claimant: alice})
	require.ErrorIs(t, err, projects.ErrRepositoryClaimed)
	_, err = c.LinkProject(ctx, projects.Link{Project: shop, Config: config})
	require.ErrorIs(t, err, projects.ErrNoClaimant)
	_, err = c.Project(ctx, "QmNobody")
	require.ErrorIs(t, err, projects.ErrNotLinked)
	_, err = c.Project(ctx, "a/b")
	require.ErrorIs(t, err, projects.ErrSeparator)
	_, err = c.GrantRole(ctx, shop, "bob", "admin", alice)
	require.ErrorIs(t, err, projects.ErrNoRole)
	require.ErrorIs(t, c.Revoke(ctx, shop, "never", alice), projects.ErrNoGrant)
}

func TestNoAuthInReachIsAnError(t *testing.T) {
	c := projects.On(newNode(t))
	t.Cleanup(func() { c.Close() })
	_, err := c.Project(t.Context(), shop)
	require.ErrorIs(t, err, service.ErrNoneRuns)
}
