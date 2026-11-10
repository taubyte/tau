package hosted

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/p2p/peer/peertest"
	secretsService "github.com/taubyte/tau/services/secrets"
)

func TestEveryBackingOpensItsDatabaseByName(t *testing.T) {
	type opener func(*node) (any, error)
	for name, open := range map[string]opener{
		cacheDatabase:     func(n *node) (any, error) { return cacheStores{node: n.Service}.Store() },
		admissionDatabase: func(n *node) (any, error) { return admissionStores{node: n.Service}.Store() },
		volumeDatabase:    func(n *node) (any, error) { return volumeStores{node: n.Service}.Store() },
	} {
		t.Run(name, func(t *testing.T) {
			refused := errors.New("disk full")
			_, err := open(overDouble(t, &memFactory{err: refused}))
			require.ErrorIs(t, err, refused)

			factory := &memFactory{}
			n := overDouble(t, factory)
			rows, err := open(n)
			require.NoError(t, err)
			require.NotNil(t, rows)
			opened, _ := factory.counts()
			require.Equal(t, []string{name}, opened)
			require.Len(t, n.closers, 1, "the database is closed with the node")
		})
	}
}

func TestTheVolumeBackingIsOnePerNode(t *testing.T) {
	none, err := (New()).disksOrNil()
	require.NoError(t, err)
	require.Nil(t, none)

	n := &Service{root: t.TempDir()}
	first, err := n.disksOrNil()
	require.NoError(t, err)
	require.NotNil(t, first)
	again, err := n.disksOrNil()
	require.NoError(t, err)
	require.Same(t, first, again)
	require.Len(t, n.closers, 1)
}

func TestADatabaseStoreIsOpenedAndReleasedByName(t *testing.T) {
	ctx := t.Context()
	refused := errors.New("disk full")
	broken := databaseStores{node: overDouble(t, &memFactory{err: refused}).Service}
	_, err := broken.Store("orders")
	require.ErrorIs(t, err, refused)
	require.ErrorContains(t, err, "orders")
	require.ErrorIs(t, broken.Release(ctx, "orders"), refused)

	// A STORE THAT CANNOT BE DESTROYED IS NOT RELEASED QUIETLY: the copy
	// would stay on disk while the node believed it gone.
	require.ErrorContains(t, databaseStores{node: overDouble(t, &memFactory{}).Service}.Release(ctx, "orders"),
		"cannot release")
}

func TestADatabaseCredentialNeedsAVault(t *testing.T) {
	mn := peertest.Net(t)
	n, _, _ := makeNode(t, mn, Config{Services: []string{Register, Worker, Stats}})
	_, err := vaultSecrets{node: n.Service}.Secret(t.Context(), "shop", "prod", "dbpass")
	require.ErrorIs(t, err, secretsService.ErrNoSecrets)
}
