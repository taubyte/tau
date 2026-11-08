package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ipfs/go-cid"
	"github.com/ipfs/go-datastore/query"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/placement"
	"github.com/taubyte/tau/services/cm/settings"
)

// view is the placement every node of a cluster sees, changed as a test goes.
type view struct {
	mu      sync.Mutex
	members []placement.Member
}

func (v *view) set(ids ...libp2ppeer.ID) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.members = v.members[:0]
	for _, id := range ids {
		v.members = append(v.members, placement.Member{ID: id.String(), Accepts: true})
	}
}

func (v *view) get() []placement.Member {
	v.mu.Lock()
	defer v.mu.Unlock()
	return slices.Clone(v.members)
}

// writtenAlone is a root node 0 wrote while it was the region's only storage node,
// drawn until the holders placement names once the others join leave node 0 out.
func writtenAlone(t *testing.T, c *cluster, v *view, at Address) (root cid.Cid, content string, holders []string) {
	t.Helper()
	v.set(c.ids[0])
	all := make([]placement.Member, 0, len(c.ids))
	for _, id := range c.ids {
		all = append(all, placement.Member{ID: id.String(), Accepts: true})
	}
	for i := range 64 {
		content = fmt.Sprintf("the artifact, build %d", i)
		v, err := c.client(t, 0, 0).Put(t.Context(), at, fmt.Sprintf("build-%d", i), Object{}, strings.NewReader(content))
		require.NoError(t, err)
		root, err = cid.Decode(v.Record.RootCID)
		require.NoError(t, err)
		holders, err = blockHolders(root.String(), all, settings.Defaults().StorageReplicas)
		require.NoError(t, err)
		if !slices.Contains(holders, c.ids[0].String()) {
			return root, content, holders
		}
	}
	require.FailNow(t, "every draw placed node 0 among its own holders")
	return
}

func readOn(t *testing.T, c *cluster, i int, root cid.Cid) (string, error) {
	t.Helper()
	r, err := c.client(t, i, i).Read(t.Context(), root.String())
	if err != nil {
		return "", err
	}
	defer r.Close()
	got, err := io.ReadAll(r)
	return string(got), err
}

func TestARootWrittenBeforeTheRegionGrewReachesTheHoldersPlacementNamesNow(t *testing.T) {
	at := assetsOf(t, "site_code")
	v := &view{}
	c := newCluster(t, 4, fixedCatalog{at}, withMembers(v.get))

	root, content, holders := writtenAlone(t, c, v, at)
	v.set(c.ids...)

	_, err := readOn(t, c, 1, root)
	require.ErrorContains(t, err, "no holder has", "the holders a reader asks do not include the writer")

	require.NoError(t, c.nodes[0].rehome(t.Context()))

	for _, h := range holders {
		id, err := libp2ppeer.Decode(h)
		require.NoError(t, err)
		require.True(t, c.swarm.holds(id, root), "%s is a holder and has no copy", h)
		refs, err := c.node(h).local.ReferencesTo(t.Context(), root)
		require.NoError(t, err)
		require.NotEmpty(t, refs, "%s holds the bytes with no record of what for", h)
	}
	for i := range c.nodes {
		got, err := readOn(t, c, i, root)
		require.NoError(t, err, "node %d", i)
		require.Equal(t, content, got, "node %d", i)
	}
}

func TestAHolderThatCouldNotBeAskedIsAskedAgainAndAPlacementHandedOverIsNot(t *testing.T) {
	at := assetsOf(t, "site_code")
	v := &view{}
	c := newCluster(t, 4, fixedCatalog{at}, withMembers(v.get))

	root, _, holders := writtenAlone(t, c, v, at)
	v.set(c.ids...)

	unreachable, err := libp2ppeer.Decode(holders[0])
	require.NoError(t, err)
	c.swarm.mu.Lock()
	c.swarm.down[unreachable] = true
	c.swarm.mu.Unlock()
	require.Error(t, c.nodes[0].rehome(t.Context()))
	require.False(t, c.swarm.holds(unreachable, root))

	c.swarm.mu.Lock()
	c.swarm.down[unreachable] = false
	c.swarm.mu.Unlock()
	require.NoError(t, c.nodes[0].rehome(t.Context()))
	require.True(t, c.swarm.holds(unreachable, root), "a holder that was down was not asked again")

	// Every holder down: a placement already handed over asks nobody.
	c.swarm.mu.Lock()
	for _, id := range c.ids {
		c.swarm.down[id] = true
	}
	c.swarm.mu.Unlock()
	require.NoError(t, c.nodes[0].rehome(t.Context()))

	// A placement that changes is handed over again.
	v.set(c.ids[1:]...)
	require.Error(t, c.nodes[0].rehome(t.Context()))
}

func TestAPlacementIsNamedByWhoAcceptsAndHowManyCopies(t *testing.T) {
	a := []placement.Member{{ID: "b", Accepts: true}, {ID: "a", Accepts: true}, {ID: "c"}}
	b := []placement.Member{{ID: "a", Accepts: true}, {ID: "b", Accepts: true}}
	require.Equal(t, placementView(a, 3), placementView(b, 3), "order and a member that declines change nothing")
	require.NotEqual(t, placementView(b, 3), placementView(b, 2))
	require.NotEqual(t, placementView(b, 3), placementView(b[:1], 3))
}

// unlistable is a local store whose rows cannot be listed.
type unlistable struct{ *orderedMem }

func (unlistable) Query(context.Context, query.Query) (query.Results, error) {
	return nil, errors.New("the disk is gone")
}

func TestReferencesThatCannotBeListedAreAnError(t *testing.T) {
	at := assetsOf(t, "site_code")
	v := &view{}
	c := newCluster(t, 4, fixedCatalog{at}, withMembers(v.get))

	writtenAlone(t, c, v, at)
	v.set(c.ids...)
	local, err := NewLocal(unlistable{newOrderedMem()}, time.Now)
	require.NoError(t, err)
	c.nodes[0].local = local

	require.ErrorContains(t, c.nodes[0].rehome(t.Context()), "the disk is gone")
}
