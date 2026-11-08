package instance

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	cmmock "github.com/taubyte/tau/services/cm/mock"
	"github.com/taubyte/tau/services/cm/settings"
)

func TestADatabaseIsOpenedOncePerName(t *testing.T) {
	ctx := t.Context()
	d := newDatabaseSet(mockPeer(t))
	t.Cleanup(func() { d.Close() })

	a, err := d.at("orders/a")
	require.NoError(t, err)
	again, err := d.at("orders/a")
	require.NoError(t, err)
	require.Same(t, a, again, "two opens of one name would be two replicas over one store")

	held, err := d.Holding(ctx, "orders/b")
	require.NoError(t, err)
	require.False(t, held, "a database never opened here holds nothing here")

	require.NoError(t, a.Put(ctx, "/k", []byte("v")))
	held, err = d.Holding(ctx, "orders/a")
	require.NoError(t, err)
	require.True(t, held)
	paths, err := d.Paths(ctx, "orders", 1)
	require.NoError(t, err)
	require.Equal(t, []string{"orders/a"}, paths)

	// A CLOSED ONE IS FORGOTTEN, so the next ask opens it again.
	a.Close()
	reopened, err := d.at("orders/a")
	require.NoError(t, err)
	require.NotSame(t, a, reopened)
	v, err := reopened.Get(ctx, "/k")
	require.NoError(t, err)
	require.Equal(t, "v", string(v), "a restarted database reads what its peer holds")

	// CLOSING THE OLD ONE AGAIN LEAVES THE NEW ONE: it is no longer what the name
	// holds.
	a.Close()
	still, err := d.at("orders/a")
	require.NoError(t, err)
	require.Same(t, reopened, still)
}

func TestTheDatabasesMoveToTheRegionsT(t *testing.T) {
	d := newDatabaseSet(mockPeer(t))
	t.Cleanup(func() { d.Close() })
	_, err := d.at("moves")
	require.NoError(t, err)

	require.Error(t, d.setRetention(time.Minute), "a T under ten margins")
	require.Zero(t, d.retention, "a refused T changes nothing")

	require.NoError(t, d.setRetention(72*time.Hour))
	require.Equal(t, 72*time.Hour, d.retention)
	_, err = d.at("later")
	require.NoError(t, err)

	require.NoError(t, d.setRetention(0), "back to the default")
	require.Zero(t, d.retention)
}

func TestTheRegionsTIsReadFromCm(t *testing.T) {
	ctx := t.Context()
	n, _, _ := makeNode(t, Config{Services: []string{Stats}})
	require.NoError(t, n.Start(ctx))

	set := settings.Defaults()
	set.Retention = 72 * time.Hour
	cm := cmmock.New(set)
	n.applyRetention(ctx, cm)
	require.Equal(t, 72*time.Hour, n.dbs.retention)

	// A cm that cannot be reached changes nothing.
	cm.Fail(errors.New("no cm in reach"))
	n.applyRetention(ctx, cm)
	require.Equal(t, 72*time.Hour, n.dbs.retention)

	cm.Fail(nil)
	cm.Set(settings.Defaults())
	n.applyRetention(ctx, cm)
	require.Zero(t, n.dbs.retention, "back to the default")
}

func TestAGroupIsTimedByTheNodesRaftKeys(t *testing.T) {
	root := t.TempDir()
	n := &Instance{Node: mockPeer(t), cfg: Config{Root: root, Raft: quickRaft}}
	c, err := groups{n}.Group("built-in")
	require.NoError(t, err)
	t.Cleanup(func() { c.Close() })
	// LEADER WELL INSIDE raft's 30s default bootstrap.
	require.Eventually(t, c.IsLeader, 5*time.Second, 20*time.Millisecond)
}
