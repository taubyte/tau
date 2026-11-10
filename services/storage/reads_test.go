package storage

import (
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/services/cm/settings"
)

// outsider is a node of c that does not hold at's index, its clock set behind the
// others by lag.
func outsider(t *testing.T, c *cluster, at Address, lag time.Duration) int {
	t.Helper()
	shard, err := shardOf(at.Bucket())
	require.NoError(t, err)
	holders := indexHolders(shard, c.nodes[0].placement(), settings.Defaults().StorageReplicas)
	for i, n := range c.nodes {
		if !slices.Contains(holders, n.self) {
			n.clock = NewClock(func() time.Time { return time.Now().Add(-lag) }, n.cfg.MaxSkew)
			return i
		}
	}
	require.FailNow(t, "every node holds the index")
	return -1
}

// A VERSION READ THROUGH A NODE ADVANCES ITS CLOCK: an overwrite it coordinates next
// is stamped after what it read, though its own clock is behind the writer's.
func TestAnOverwriteAfterAReadIsStampedAfterWhatItRead(t *testing.T) {
	at := assetsOf(t, "site_code")
	c := newCluster(t, 4, fixedCatalog{at})
	x := outsider(t, c, at, time.Minute)

	first, err := c.client(t, (x+1)%4, (x+1)%4).Put(t.Context(), at, "page", Object{}, strings.NewReader("first"))
	require.NoError(t, err)
	through := c.client(t, x, x)
	read, err := through.Current(t.Context(), at, "page")
	require.NoError(t, err)
	require.Equal(t, first.Stamp, read.Stamp)

	second, err := through.Put(t.Context(), at, "page", Object{}, strings.NewReader("second"))
	require.NoError(t, err)
	require.Greater(t, second.Stamp, first.Stamp, "an overwrite after a read was stamped before what it read")
	current, err := through.Current(t.Context(), at, "page")
	require.NoError(t, err)
	require.Equal(t, second.Stamp, current.Stamp)
}

// A COPY HERE THAT CANNOT BE READ IS NOT THE ANSWER: the read is answered from a
// holder that can.
func TestACopyThatCannotBeReadHereIsReadFromAHolder(t *testing.T) {
	at := assetsOf(t, "site_code")
	c := newCluster(t, 3, fixedCatalog{at})
	v, err := c.client(t, 0, 0).Put(t.Context(), at, "page", Object{}, strings.NewReader("the page"))
	require.NoError(t, err)
	c.swarm.mu.Lock()
	c.swarm.unreadable[c.ids[0]] = true
	c.swarm.mu.Unlock()

	r, err := c.client(t, 0, 0).Read(t.Context(), v.Record.RootCID)
	require.NoError(t, err)
	got, err := io.ReadAll(r)
	require.NoError(t, r.Close())
	require.NoError(t, err)
	require.Equal(t, "the page", string(got))

	// Asked for its own copy only, it says it cannot.
	root, err := cid.Decode(v.Record.RootCID)
	require.NoError(t, err)
	_, err = c.nodes[0].open(t.Context(), root, true)
	require.Error(t, err)
}

// A COPY OF THE INDEX THAT HOLDS NOTHING OF AN INSTANCE ANSWERS NOTHING: a holder
// placement just moved the shard to, which has merged nothing yet, sends the read on
// rather than call every object not found.
func TestACopyHoldingNothingOfAnInstanceAnswersNothing(t *testing.T) {
	at := assetsOf(t, "site_code")
	c := newCluster(t, 3, fixedCatalog{at})
	_, err := c.client(t, 0, 0).Put(t.Context(), at, "page", Object{}, strings.NewReader("the page"))
	require.NoError(t, err)
	behind := shardOn(t, c, 2, at)
	prefix, err := at.objectsPrefix()
	require.NoError(t, err)
	rows, err := index{store: behind}.all(t.Context(), prefix, 0)
	require.NoError(t, err)
	require.NotEmpty(t, rows)
	for _, r := range rows {
		require.NoError(t, behind.Delete(t.Context(), ds.RawKey(r.key)))
	}

	through := c.client(t, 2, 2)
	v, err := through.Current(t.Context(), at, "page")
	require.NoError(t, err, "a copy holding nothing called an object not found")
	require.Equal(t, "page", v.Name)
	listed, err := through.List(t.Context(), at, "", "", 0)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	versions, err := through.Versions(t.Context(), at, "page", 0)
	require.NoError(t, err)
	require.Len(t, versions, 1)
	_, err = c.nodes[2].current(t.Context(), at, "page", true)
	require.ErrorIs(t, err, ErrEmptyCopy)

	// An object nobody wrote is still not found, and an instance nobody wrote lists
	// nothing.
	_, err = through.Current(t.Context(), at, "absent")
	require.ErrorIs(t, err, ErrNotFound)
	nobody := assetsOf(t, "nobody_wrote")
	_, err = through.Current(t.Context(), nobody, "page")
	require.ErrorIs(t, err, ErrNotFound)
	listed, err = through.List(t.Context(), nobody, "", "", 0)
	require.NoError(t, err)
	require.Empty(t, listed)
}

// A DELETE SUPERSEDES WHAT IT DELETES, from a node whose clock is behind the writer's
// and that read nothing first.
func TestADeleteFromABehindClockSupersedesWhatItDeletes(t *testing.T) {
	at := assetsOf(t, "site_code")
	c := newCluster(t, 4, fixedCatalog{at})
	x := outsider(t, c, at, time.Minute)

	_, err := c.client(t, (x+1)%4, (x+1)%4).Put(t.Context(), at, "page", Object{}, strings.NewReader("first"))
	require.NoError(t, err)
	_, err = c.client(t, x, x).Delete(t.Context(), at, "page")
	require.NoError(t, err)
	_, err = c.client(t, 0, 0).Current(t.Context(), at, "page")
	require.ErrorIs(t, err, ErrNotFound, "a delete stamped before the version it deleted")
}

// AN INSTANCE'S SIZE IS ASKED OF ANY NODE: one outside the index asks a holder, and an
// instance nobody wrote holds nothing.
func TestAnInstancesSizeIsAnsweredFromAnyNode(t *testing.T) {
	at := assetsOf(t, "site_code")
	c := newCluster(t, 4, fixedCatalog{at})
	x := outsider(t, c, at, 0)
	_, err := c.client(t, 0, 0).Put(t.Context(), at, "page", Object{Versioned: true}, strings.NewReader("first"))
	require.NoError(t, err)
	_, err = c.client(t, 0, 0).Put(t.Context(), at, "page", Object{Versioned: true}, strings.NewReader("second!"))
	require.NoError(t, err)

	n, err := c.client(t, x, x).Size(t.Context(), at)
	require.NoError(t, err)
	require.Equal(t, uint64(len("first")+len("second!")), n)
	n, err = c.client(t, x, x).Size(t.Context(), assetsOf(t, "nobody_wrote"))
	require.NoError(t, err)
	require.Zero(t, n)
}
