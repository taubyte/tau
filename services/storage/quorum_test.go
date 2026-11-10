package storage

import (
	"fmt"
	"strings"
	"testing"
	"time"

	ds "github.com/ipfs/go-datastore"
	"github.com/stretchr/testify/require"
)

// missed takes the row v wrote out of node i's copy of at's index, as a copy that was
// down when the write was confirmed on the others.
func missed(t *testing.T, c *cluster, i int, at Address, v Version) {
	t.Helper()
	key, err := at.versionKey(v.Name, v.Stamp, v.Writer)
	require.NoError(t, err)
	require.NoError(t, shardOn(t, c, i, at).Delete(t.Context(), ds.RawKey(key)))
}

// A READ SEES A WRITE ITS NODE'S COPY MISSED: it hears two of the three copies, and one
// of any two confirmed the write.
func TestAReadSeesAWriteItsCopyMissed(t *testing.T) {
	at := assetsOf(t, "site_code")
	c := newCluster(t, 3, fixedCatalog{at})
	first, err := c.client(t, 0, 0).Put(t.Context(), at, "page", Object{}, strings.NewReader("first"))
	require.NoError(t, err)
	second, err := c.client(t, 0, 0).Put(t.Context(), at, "page", Object{}, strings.NewReader("second"))
	require.NoError(t, err)
	missed(t, c, 2, at, second)

	behind := c.client(t, 2, 2)
	v, err := behind.Current(t.Context(), at, "page")
	require.NoError(t, err)
	require.Equal(t, second.Stamp, v.Stamp, "a read answered from the copy that missed the write")
	listed, err := behind.List(t.Context(), at, "", "", 0)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.Equal(t, second.Stamp, listed[0].Stamp)
	versions, err := behind.Versions(t.Context(), at, "page", 0)
	require.NoError(t, err)
	require.Equal(t, []uint64{second.Stamp, first.Stamp}, []uint64{versions[0].Stamp, versions[1].Stamp})
}

// A DELETE A COPY MISSED STAYS DELETED: the newest row the copies hold is the
// tombstone, and the copy still holding the version does not bring it back.
func TestADeleteACopyMissedStaysDeleted(t *testing.T) {
	at := assetsOf(t, "site_code")
	c := newCluster(t, 3, fixedCatalog{at})
	_, err := c.client(t, 0, 0).Put(t.Context(), at, "page", Object{}, strings.NewReader("the page"))
	require.NoError(t, err)
	_, err = c.client(t, 0, 0).Put(t.Context(), at, "kept", Object{}, strings.NewReader("kept"))
	require.NoError(t, err)
	tomb, err := c.client(t, 0, 0).Delete(t.Context(), at, "page")
	require.NoError(t, err)
	missed(t, c, 2, at, tomb)

	behind := c.client(t, 2, 2)
	_, err = behind.Current(t.Context(), at, "page")
	require.ErrorIs(t, err, ErrNotFound, "a copy that missed the delete brought the version back")
	listed, err := behind.List(t.Context(), at, "", "", 0)
	require.NoError(t, err)
	require.Equal(t, []string{"kept"}, names(listed))
}

// A READ THAT CANNOT HEAR ENOUGH COPIES FAILS rather than answer from one that may be
// behind; with one of three down it still answers.
func TestAReadNeedsEnoughCopies(t *testing.T) {
	at := assetsOf(t, "site_code")
	c := newCluster(t, 3, fixedCatalog{at})
	_, err := c.client(t, 0, 0).Put(t.Context(), at, "page", Object{}, strings.NewReader("the page"))
	require.NoError(t, err)

	require.NoError(t, c.nodes[0].Close())
	_, err = c.client(t, 2, 2).Current(t.Context(), at, "page")
	require.NoError(t, err, "one copy of three down stopped a read")

	require.NoError(t, c.nodes[1].Close())
	_, err = c.client(t, 2, 2).Current(t.Context(), at, "page")
	require.Error(t, err, "a read answered from one copy of three")
	require.NotErrorIs(t, err, ErrNotFound)
	_, err = c.client(t, 2, 2).List(t.Context(), at, "", "", 0)
	require.Error(t, err)
}

// A CLOUD OF ONE OR TWO NODES READS ONE COPY: a write there is on every copy, so any
// one answers for it, and two nodes still read with one of them down.
func TestASmallCloudReadsOneCopy(t *testing.T) {
	for _, n := range []int{1, 2} {
		t.Run(fmt.Sprintf("%d nodes", n), func(t *testing.T) {
			at := assetsOf(t, "site_code")
			c := newCluster(t, n, fixedCatalog{at})
			holders, q, err := c.nodes[0].readQuorum(t.Context(), at)
			require.NoError(t, err)
			require.Len(t, holders, n)
			require.Equal(t, 1, q)

			v, err := c.client(t, 0, 0).Put(t.Context(), at, "page", Object{}, strings.NewReader("the page"))
			require.NoError(t, err)
			if n == 2 {
				require.NoError(t, c.nodes[1].Close())
			}
			got, err := c.client(t, 0, 0).Current(t.Context(), at, "page")
			require.NoError(t, err)
			require.Equal(t, v.Stamp, got.Stamp)
		})
	}
	at := assetsOf(t, "site_code")
	c := newCluster(t, 4, fixedCatalog{at})
	holders, q, err := c.nodes[0].readQuorum(t.Context(), at)
	require.NoError(t, err)
	require.Len(t, holders, 3)
	require.Equal(t, 2, q, "three holders, two confirming a write, two to read")
}

// A LISTING PAGES ACROSS COPIES: a page one copy cut short is complete only up to its
// last name, so every name comes back once, in order, through a copy that missed one.
func TestAListingPagesAcrossCopies(t *testing.T) {
	at := assetsOf(t, "site_code")
	c := newCluster(t, 3, fixedCatalog{at})
	var written []Version
	for i := range 7 {
		v, err := c.client(t, 0, 0).Put(t.Context(), at, fmt.Sprintf("n%d", i), Object{}, strings.NewReader(fmt.Sprint(i)))
		require.NoError(t, err)
		written = append(written, v)
	}
	missed(t, c, 2, at, written[3])

	behind := c.client(t, 2, 2)
	var got []string
	after := ""
	for range 10 {
		page, err := behind.List(t.Context(), at, "", after, 2)
		require.NoError(t, err)
		if len(page) == 0 {
			break
		}
		require.LessOrEqual(t, len(page), 2)
		got = append(got, names(page)...)
		after = page[len(page)-1].Name
	}
	require.Equal(t, []string{"n0", "n1", "n2", "n3", "n4", "n5", "n6"}, got)
}

// THE PAGES MERGE ON NAME AND STAMP: the newest of each name decides, a tombstone or an
// expired version hides it, and names past the first page cut short wait.
func TestPagesMergeOnTheNewestOfEachName(t *testing.T) {
	at := time.UnixMilli(1_800_000_000_000)
	v := func(name string, stamp uint64, r VersionRecord) Version {
		return Version{VersionKey: VersionKey{Name: name, Stamp: stamp, WriterSegment: "w"}, Record: r}
	}
	live, bound, more := mergePages([][]Version{
		{v("a", 1, VersionRecord{}), v("b", 1, VersionRecord{}), v("c", 2, VersionRecord{})},
		{v("a", 2, VersionRecord{}), v("b", 2, VersionRecord{Tombstone: true}), v("d", 1, VersionRecord{})},
	}, 3, at)
	require.True(t, more)
	require.Equal(t, "c", bound, "the earliest cut bounds what is complete")
	require.Equal(t, []string{"a", "c"}, names(live))
	require.Equal(t, uint64(2), live[0].Stamp, "the newest of a name decides")

	live, _, more = mergePages([][]Version{
		{v("x", 1, VersionRecord{ExpiresMS: uint64(at.UnixMilli())}), v("y", 1, VersionRecord{})},
	}, 0, at)
	require.False(t, more)
	require.Equal(t, []string{"y"}, names(live), "an expired version was listed")

	newest, ok := newestOf([]copyRow{{}, {v: v("a", 3, VersionRecord{}), ok: true}, {v: v("a", 2, VersionRecord{}), ok: true}})
	require.True(t, ok)
	require.Equal(t, uint64(3), newest.Stamp)
	_, ok = newestOf([]copyRow{{}, {}})
	require.False(t, ok)

	union := unionOf([][]Version{{v("a", 3, VersionRecord{}), v("a", 1, VersionRecord{})}, {v("a", 3, VersionRecord{}), v("a", 2, VersionRecord{})}}, 2)
	require.Equal(t, []uint64{3, 2}, []uint64{union[0].Stamp, union[1].Stamp})
}

// A LISTING SKIPS NO NAME ONE COPY MISSED: one copy's page, short a name, reaches past
// another's, and a tombstone frees a slot in it; the names past the shorter page wait
// for the next one rather than be taken now and skip what lies between.
func TestAListingSkipsNoNameOneCopyMissed(t *testing.T) {
	at := assetsOf(t, "site_code")
	c := newCluster(t, 3, fixedCatalog{at})
	put := func(name string) Version {
		t.Helper()
		v, err := c.client(t, 0, 0).Put(t.Context(), at, name, Object{}, strings.NewReader(name))
		require.NoError(t, err)
		return v
	}
	put("a")
	b, bb := put("b"), put("bb")
	put("c")
	put("d")
	_, err := c.client(t, 0, 0).Delete(t.Context(), at, "a")
	require.NoError(t, err)
	missed(t, c, 2, at, b)
	missed(t, c, 2, at, bb)

	behind := c.client(t, 2, 2)
	var got []string
	after := ""
	for range 10 {
		page, err := behind.List(t.Context(), at, "", after, 2)
		require.NoError(t, err)
		if len(page) == 0 {
			break
		}
		got = append(got, names(page)...)
		after = page[len(page)-1].Name
	}
	require.Equal(t, []string{"b", "bb", "c", "d"}, got)
}
