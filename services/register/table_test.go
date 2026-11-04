package register

import (
	"testing"

	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/p2p/peer"
	pkgregister "github.com/taubyte/tau/pkg/register"
)

func TestTheTableIsTheRegisterBesideIt(t *testing.T) {
	n := mockNode(t)
	s := startRegister(t, n)
	n.running = []service.Running{{Name: Descriptor.Name, Service: s}}

	table := TableOn(n)
	t.Cleanup(func() { table.Close() })
	got, err := table.Now(t.Context())
	require.NoError(t, err)
	require.Same(t, s.Local(), got, "the register's own store, no client")
	_, held := table.At()
	require.False(t, held, "nothing was dialled")
}

func TestTheTableReachesARegisterElsewhere(t *testing.T) {
	ctx := t.Context()
	at, from := mockNode(t), mockNode(t)
	startRegister(t, at)
	require.NoError(t, peer.LinkAllPeers())
	require.NoError(t, from.Host().Connect(ctx, libp2ppeer.AddrInfo{ID: at.ID(), Addrs: at.Host().Addrs()}))
	from.running = []service.Running{{Name: "discovery", Service: directory{at.ID()}}}

	table := TableOn(from)
	t.Cleanup(func() { table.Close() })
	got, err := table.Now(ctx)
	require.NoError(t, err)
	envs, err := got.Environments(ctx, "shop")
	require.NoError(t, err)
	require.Equal(t, []string{"prod"}, envs)
	_, err = got.Project(ctx, "QmNobody")
	require.ErrorIs(t, err, pkgregister.ErrNotLinked)
}
