package secrets

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	peerCore "github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// localClient is a client on a member, writing through that member's own
// service the way a node running the service does.
func (c *cluster) localClient(t *testing.T, i int) *Client {
	t.Helper()

	srv := c.services[i]
	client, err := NewClient(c.nodes[i].Peer(), func() []peerCore.ID { return nil }, func() *Service { return srv })
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })
	return client
}

// versionsOn is how many versions of ref one node stores.
func versionsOn(t *testing.T, ctx context.Context, srv *Service, ref Ref) int {
	t.Helper()

	versions, err := srv.records.Versions(ctx, ref)
	require.NoError(t, err)
	return len(versions)
}

// TestARewriteAfterRotationIsReadEverywhere is the regression for a re-Set
// after several members carried a secret forward. The record used to be one
// key, so the rewrite replaced its ciphertext while the carry-forward wrappings
// of the old data key stayed beside it; a reader paired the new ciphertext with
// an old wrapping and the secret was unreadable for good.
func TestARewriteAfterRotationIsReadEverywhere(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := newCluster(t, ctx, 3)
	c.bootstrap(t, ctx, 0, 1, 2)
	first := c.awaitKey(t, ctx)

	consumer := c.client(t)
	require.NoError(t, consumer.Set(ctx, ref("rewritten"), []byte("v1")))

	c.grow(t, ctx)
	require.Eventually(t, func() bool {
		c.reconcileAll(ctx)
		active, err := c.services[0].ActiveKey(ctx)
		if err != nil || active.Epoch == first.Epoch {
			return false
		}
		_, _, err = c.services[0].wrappings.Get(ctx, newest(ctx, c.services[0], ref("rewritten")), active.KeyID)
		return err == nil
	}, 60*time.Second, 200*time.Millisecond, "the secret was never carried forward")

	highest := 0
	for i, node := range c.nodes {
		if node.ID().String() > c.nodes[highest].ID().String() {
			highest = i
		}
	}
	require.NoError(t, c.localClient(t, highest).Set(ctx, ref("rewritten"), []byte("v2")))

	require.Eventually(t, func() bool {
		c.reconcileAll(ctx)
		for _, srv := range c.services {
			if versionsOn(t, ctx, srv, ref("rewritten")) != 1 {
				return false
			}
		}
		got, err := consumer.Get(ctx, ref("rewritten"))
		return err == nil && string(got) == "v2"
	}, 60*time.Second, 200*time.Millisecond, "the rewrite was not the one value left")

	for i := range c.services {
		got, err := c.localClient(t, i).Get(ctx, ref("rewritten"))
		require.NoError(t, err)
		assert.Equal(t, "v2", string(got), "node %d", i)
	}
}

// skewed gives the nodes of a cluster, in start order, clocks this far off.
func skewed(offsets ...time.Duration) Option {
	var started atomic.Int32
	return func(o *options) error {
		offset := offsets[int(started.Add(1)-1)%len(offsets)]
		o.now = func() time.Time { return time.Now().Add(offset) }
		return nil
	}
}

// TestASequentialRewriteWinsWhateverTheClocks: the second write comes from a
// node whose clock is two hours behind the first writer's. It still wins,
// because a version is never stamped earlier than the newest one its writer has
// seen.
func TestASequentialRewriteWinsWhateverTheClocks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := newCluster(t, ctx, 2, skewed(time.Hour, -time.Hour))
	c.bootstrap(t, ctx, 0, 1)
	c.awaitKey(t, ctx)

	require.NoError(t, c.localClient(t, 0).Set(ctx, ref("clocked"), []byte("first")))
	require.Eventually(t, func() bool {
		return versionsOn(t, ctx, c.services[1], ref("clocked")) == 1
	}, settleFor, 100*time.Millisecond)
	require.NoError(t, c.localClient(t, 1).Set(ctx, ref("clocked"), []byte("second")))

	consumer := c.client(t)
	require.Eventually(t, func() bool {
		for i := range c.services {
			got, err := c.localClient(t, i).Get(ctx, ref("clocked"))
			if err != nil || string(got) != "second" {
				return false
			}
		}
		got, err := consumer.Get(ctx, ref("clocked"))
		return err == nil && string(got) == "second"
	}, settleFor, 100*time.Millisecond, "the earlier write won on its clock")

	require.Eventually(t, func() bool {
		c.reconcileAll(ctx)
		return versionsOn(t, ctx, c.services[0], ref("clocked")) == 1 && versionsOn(t, ctx, c.services[1], ref("clocked")) == 1
	}, settleFor, 200*time.Millisecond, "the superseded version was not pruned")
}

// TestANewRosterRotatesOnceOnItsOwn: a member joining is noticed without a
// reconcile tick, confirmed by a second pass a moment later, and costs exactly
// one new epoch.
func TestANewRosterRotatesOnceOnItsOwn(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := newCluster(t, ctx, 2, WithBootstrapDelay(100*time.Millisecond))
	first := c.awaitKey(t, ctx)
	require.Len(t, first.Members, 2)

	c.grow(t, ctx)

	require.Eventually(t, func() bool {
		for _, srv := range c.services {
			active, err := srv.ActiveKey(ctx)
			if err != nil || len(active.Members) != 3 {
				return false
			}
		}
		return true
	}, 60*time.Second, 100*time.Millisecond, "the cluster never rotated to its new roster")

	// Give a second, competing round every chance to appear.
	time.Sleep(time.Second)
	for _, srv := range c.services {
		keys, err := srv.keys.List(ctx)
		require.NoError(t, err)
		var newer []string
		for _, key := range keys {
			if key.Epoch > first.Epoch {
				newer = append(newer, key.KeyID)
			}
		}
		assert.Len(t, newer, 1, "one membership change made more than one key")
	}
}

// TestAProjectNamedCloudCannotReachTheCloudScope: the scope kind is a path
// segment of its own, so no project name spells a cloud secret's key.
func TestAProjectNamedCloudCannotReachTheCloudScope(t *testing.T) {
	_, client, _ := single(t)
	ctx := t.Context()

	cloud := Ref{Scope: Cloud("auth"), Name: "signing"}
	impostor := Ref{Scope: Project("cloud", "auth"), Name: "signing"}
	require.NoError(t, client.Set(ctx, cloud, []byte("the cloud's")))

	_, err := client.Get(ctx, impostor)
	require.ErrorIs(t, err, ErrNotSet)
	names, err := client.List(ctx, impostor.Scope)
	require.NoError(t, err)
	assert.Empty(t, names)

	require.NoError(t, client.Set(ctx, impostor, []byte("a project's")))
	got, err := client.Get(ctx, cloud)
	require.NoError(t, err)
	assert.Equal(t, "the cloud's", string(got))
	names, err = client.List(ctx, Cloud("auth"))
	require.NoError(t, err)
	assert.Equal(t, []string{"signing"}, names)

	require.ErrorIs(t, Scope{Service: "auth", Project: "p"}.Validate(), ErrInvalidRef)
	require.ErrorIs(t, Cloud("a/b").Validate(), ErrInvalidRef)
	_, err = client.List(ctx, Scope{})
	require.ErrorIs(t, err, ErrInvalidRef)
}

// TestANewerVersionThisNodeCannotOpenIsKept: pruning keeps what it cannot
// prove is superseded, and a read serves the newest version that opens.
func TestANewerVersionThisNodeCannotOpenIsKept(t *testing.T) {
	srv, client, _ := single(t)
	ctx := t.Context()

	r := Ref{Scope: Project("p", "e"), Name: "n"}
	require.NoError(t, client.Set(ctx, r, []byte("old")))
	require.NoError(t, client.Set(ctx, r, []byte("current")))

	// A version newer than both, with no wrapping this node can use: the state
	// of one written elsewhere whose wrapping has not arrived.
	unopenable := []byte("not yet")
	require.NoError(t, srv.records.Put(ctx, version{Ref: r, CID: contentID(unopenable)}, unopenable, time.Now().Add(time.Hour).UnixNano()))
	require.Equal(t, 3, versionsOn(t, ctx, srv, r))

	require.Error(t, srv.reconcile(ctx), "a version that cannot be carried forward is reported")
	versions, err := srv.records.Versions(ctx, r)
	require.NoError(t, err)
	require.Len(t, versions, 2, "the newer unopenable version and the newest that opens are kept")
	assert.Equal(t, contentID(unopenable), versions[0].CID)

	got, err := client.Get(ctx, r)
	require.NoError(t, err)
	assert.Equal(t, "current", string(got))

	require.NoError(t, client.Delete(ctx, r))
	assert.Zero(t, versionsOn(t, ctx, srv, r), "a delete removes every version")
}

// TestAnUnreadableSecretDoesNotStopRetirement: one version that cannot be
// carried forward is reported, and every other secret's old wrappings still go.
func TestAnUnreadableSecretDoesNotStopRetirement(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const age = time.Hour
	clock := newFakeClock()
	c := newCluster(t, ctx, 2, WithRotationAge(age), WithClock(clock.now))
	c.bootstrap(t, ctx, 0)
	first := c.awaitKey(t, ctx)

	consumer := c.client(t)
	require.NoError(t, consumer.Set(ctx, ref("good"), []byte("v")))
	stuck := []byte("wrapped under nothing")
	require.NoError(t, c.services[0].records.Put(ctx, version{Ref: ref("stuck"), CID: contentID(stuck)}, stuck, 1))

	clock.advance(age + time.Minute)
	require.Eventually(t, func() bool {
		c.reconcileAll(ctx)
		keyIDs, err := c.services[0].wrappings.KeyIDs(ctx, newest(ctx, c.services[0], ref("good")))
		return err == nil && len(keyIDs) == 1 && keyIDs[0] != first.KeyID
	}, 60*time.Second, 200*time.Millisecond, "the good secret's old wrapping was never retired")

	require.Error(t, c.services[0].reconcile(ctx))
	got, err := consumer.Get(ctx, ref("good"))
	require.NoError(t, err)
	assert.Equal(t, "v", string(got))
}
