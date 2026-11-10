package register

import (
	"testing"

	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/require"
	"github.com/taubyte/tau/p2p/peer/peertest"
	pkgregister "github.com/taubyte/tau/pkg/register"
)

func TestTheTableIsTheRegisterBesideIt(t *testing.T) {
	n := mockNode(t)
	s := startRegister(t, n)

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
	require.NoError(t, peertest.LinkAll(t))
	require.NoError(t, from.Host().Connect(ctx, libp2ppeer.AddrInfo{ID: at.ID(), Addrs: at.Host().Addrs()}))
	from.finding = finding{at.ID()}

	table := TableOn(from)
	t.Cleanup(func() { table.Close() })
	got, err := table.Now(ctx)
	require.NoError(t, err)
	envs, err := got.Environments(ctx, "shop")
	require.NoError(t, err)
	require.Equal(t, []string{"prod"}, envs)
	_, err = got.Resolve(ctx, "QmNobody", "prod")
	require.ErrorIs(t, err, pkgregister.ErrNotPublished)
}
