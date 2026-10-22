package secrets

import (
	"context"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	logging "github.com/ipfs/go-log/v2"
	peerCore "github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/kvdb"
	secretsv2 "github.com/taubyte/tau/pkg/proto/gen/tau/secrets/v2"
	"github.com/taubyte/tau/pkg/secrets/tdh"
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

// initOn writes a bundle into one node's store through a service that is not
// serving, the way `taud init` does on a stopped node.
func initOn(t *testing.T, ctx context.Context, c *cluster, i int, bundle *secretsv2.InitBundle) {
	t.Helper()

	srv, err := New(c.nodes[i].Peer(), c.dbs[i], WithRoot(c.paths[i]))
	require.NoError(t, err)
	require.NoError(t, srv.Init(ctx, bundle))
	require.NoError(t, srv.Close())
}

func bundleOf(t *testing.T, values map[Ref]string) *secretsv2.InitBundle {
	t.Helper()

	bundle := &secretsv2.InitBundle{}
	for ref, value := range values {
		sealed, err := Sealed(ref, []byte(value), time.Now())
		require.NoError(t, err)
		bundle.Secrets = append(bundle.Secrets, sealed)
	}
	return bundle
}

// TestInitOnEveryNodeSurvivesOneLostBeforeStart is the regression for the
// seeding flow. Every node is handed the same sealed bundle and writes it under
// a one-member key of its own; one node never starts. The survivors rotate to
// their real roster, every secret stays readable, and the one-member keys'
// wrappings and shares are gone.
//
// It used to be that each node wrote its own ciphertext under its own key, so a
// secret whose only writer died was unreadable forever, and the error carrying
// it forward returned before retirement ran at all.
func TestInitOnEveryNodeSurvivesOneLostBeforeStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := newMesh(t, ctx, 3)
	for _, node := range c.nodes {
		db, err := kvdb.New(node).New(logging.Logger("secrets-test"), c.db, 1)
		require.NoError(t, err)
		t.Cleanup(func() { db.Close() })
		c.dbs = append(c.dbs, db)
		c.paths = append(c.paths, t.TempDir())
	}

	values := map[Ref]string{
		{Scope: Cloud("auth"), Name: "oauth-github.com"}: "client-secret",
		{Scope: Cloud("cm"), Name: "acme-account"}:       "account-key",
	}
	bundle := bundleOf(t, values)
	for i := range c.nodes {
		initOn(t, ctx, c, i, bundle)
	}

	for i := range 2 {
		c.launch(t, c.nodes[i], c.dbs[i], c.paths[i])
	}

	var genesis []string
	for _, srv := range c.services {
		key := mustActive(t, ctx, srv)
		require.Equal(t, []string{srv.self()}, key.Members)
		genesis = append(genesis, key.KeyID)
	}

	consumer := c.client(t)
	var real *KeyMeta
	require.Eventually(t, func() bool {
		c.reconcileAll(ctx)
		for _, srv := range c.services {
			active, err := srv.ActiveKey(ctx)
			if err != nil || len(active.Members) != 2 {
				return false
			}
			real = active
			for ref := range values {
				keyIDs, err := srv.wrappings.KeyIDs(ctx, newest(ctx, srv, ref))
				if err != nil || !slices.Equal(keyIDs, []string{active.KeyID}) {
					return false
				}
			}
			for _, id := range genesis {
				if _, err := srv.shares.Get(ctx, id); err == nil {
					return false
				}
			}
		}
		return true
	}, 60*time.Second, 200*time.Millisecond, "the one-member keys were never retired")

	for _, srv := range c.services {
		require.NoError(t, srv.reconcile(ctx), "a converged pass reports nothing")
		for ref := range values {
			assert.Equal(t, 1, versionsOn(t, ctx, srv, ref), "every node wrote the same version")
		}
	}
	for ref, value := range values {
		got, err := consumer.Get(ctx, ref)
		require.NoError(t, err)
		assert.Equal(t, value, string(got))
	}
	assert.Len(t, real.Members, 2)
}

// TestInitOnANodeHoldingAKeyWritesUnderIt: a node that already holds a real
// key wraps under it, and no epoch is started.
func TestInitOnANodeHoldingAKeyWritesUnderIt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := newCluster(t, ctx, 2)
	c.bootstrap(t, ctx, 0)
	key := c.awaitKey(t, ctx)

	require.NoError(t, c.services[1].Close())
	r := Ref{Scope: Cloud("auth"), Name: "pepper"}
	initOn(t, ctx, c, 1, bundleOf(t, map[Ref]string{r: "salt"}))

	keys, err := c.services[1].keys.List(ctx)
	require.NoError(t, err)
	assert.Len(t, keys, 1, "init started an epoch on a node that held a key")
	keyIDs, err := c.services[1].wrappings.KeyIDs(ctx, newest(ctx, c.services[1], r))
	require.NoError(t, err)
	assert.Equal(t, []string{key.KeyID}, keyIDs)

	c.restart(t, 1)
	got, err := c.client(t).Get(ctx, r)
	require.NoError(t, err)
	assert.Equal(t, "salt", string(got))
}

// TestInitRefusesABundleThatDoesNotHoldTogether.
func TestInitRefusesABundleThatDoesNotHoldTogether(t *testing.T) {
	srv, _, _ := single(t)
	ctx := t.Context()

	_, err := Sealed(Ref{}, []byte("v"), time.Now())
	require.ErrorIs(t, err, ErrInvalidRef)

	r := Ref{Scope: Cloud("auth"), Name: "n"}
	good, err := Sealed(r, []byte("v"), time.Now())
	require.NoError(t, err)

	for name, change := range map[string]func(*secretsv2.InitSecret){
		"no ref":      func(s *secretsv2.InitSecret) { s.Ref = nil },
		"another cid": func(s *secretsv2.InitSecret) { s.Cid = contentID([]byte("other")) },
		"another dek": func(s *secretsv2.InitSecret) { s.Dek = make([]byte, len(s.Dek)) },
		"moved":       func(s *secretsv2.InitSecret) { s.Ref = wireRef(Ref{Scope: Cloud("cm"), Name: "n"}) },
	} {
		t.Run(name, func(t *testing.T) {
			bad := &secretsv2.InitSecret{Ref: good.Ref, Cid: good.Cid, Ciphertext: good.Ciphertext, Dek: good.Dek, WrittenAt: good.WrittenAt}
			change(bad)
			require.Error(t, srv.Init(ctx, &secretsv2.InitBundle{Secrets: []*secretsv2.InitSecret{bad}}))
		})
	}
	all, err := srv.records.All(ctx)
	require.NoError(t, err)
	assert.Empty(t, all, "a refused secret was written")

	require.NoError(t, srv.Init(ctx, &secretsv2.InitBundle{Secrets: []*secretsv2.InitSecret{good}}))
	assert.Equal(t, 1, versionsOn(t, ctx, srv, r))
	_, err = tdh.Unseal(good.Dek, good.Ciphertext, contentAAD(r))
	require.NoError(t, err)
}

// TestVersionStepsReportAFailingStore: init, set and delete surface a store
// failure rather than write half of a version.
func TestVersionStepsReportAFailingStore(t *testing.T) {
	srv, _, _ := single(t)
	ctx := t.Context()
	r := Ref{Scope: Cloud("auth"), Name: "n"}
	bundle := bundleOf(t, map[Ref]string{r: "v"})

	failing := &Service{host: srv.host, shares: srv.shares, now: time.Now, ctx: ctx, rounds: map[string]*round{},
		db: broken{}, keys: &keyStore{db: broken{}}, records: &recordStore{db: broken{}}, wrappings: &wrappingStore{db: broken{}}}
	require.ErrorIs(t, failing.Init(ctx, bundle), errBroken)
	require.ErrorIs(t, failing.Delete(ctx, r), errBroken)
	require.ErrorIs(t, failing.dropVersion(ctx, version{Ref: r, CID: "c"}), errBroken)
	_, err := failing.nextWrittenAt(ctx, r)
	require.ErrorIs(t, err, errBroken)
	_, _, _, _, err = failing.Get(ctx, r)
	require.ErrorIs(t, err, errBroken)

	require.NoError(t, srv.Init(ctx, bundle))
	key := mustActive(t, ctx, srv)
	other := &KeyMeta{KeyID: "not-held", Epoch: key.Epoch, Public: key.Public, Threshold: 1, Members: key.Members}
	require.NoError(t, srv.keys.Put(ctx, other))
	require.ErrorContains(t, srv.Set(ctx, r, []byte("c"), other.KeyID, &tdh.Wrapping{}), "holds no share")
	require.ErrorIs(t, srv.Set(ctx, Ref{Scope: Cloud("auth")}, []byte("c"), key.KeyID, &tdh.Wrapping{}), ErrInvalidRef)
}

// TestAVersionStillHeldByARosterMemberIsNotAbandoned: the rule only fires
// for keys none of whose members are in the newest key.
func TestAVersionStillHeldByARosterMemberIsNotAbandoned(t *testing.T) {
	srv, client, _ := single(t)
	ctx := t.Context()

	r := Ref{Scope: Project("p", "e"), Name: "n"}
	require.NoError(t, client.Set(ctx, r, []byte("v")))
	v := newest(ctx, srv, r)

	assert.False(t, srv.abandoned(ctx, v, nil), "no key: nothing to judge by")
	keys, err := srv.keys.Newest(ctx)
	require.NoError(t, err)
	assert.False(t, srv.abandoned(ctx, v, keys), "wrapped under the newest key")
	later := &KeyMeta{KeyID: "later", Epoch: keys[0].Epoch + 1, Members: keys[0].Members}
	assert.False(t, srv.abandoned(ctx, v, []*KeyMeta{later}), "its member is still in the roster")
	assert.True(t, srv.abandoned(ctx, v, []*KeyMeta{{KeyID: "other", Epoch: later.Epoch, Members: []string{"someone-else"}}}))

	bare := []byte("no wrapping")
	assert.False(t, srv.abandoned(ctx, version{Ref: r, CID: contentID(bare)}, []*KeyMeta{later}), "mid-write")
}

// ownerAway stores v1 on three nodes and a newer v2 only on node 2, under
// its one-member key, then runs nodes 0 and 1 without it until they have
// rotated. away closes node 2's database for that time when set.
func ownerAway(t *testing.T, ctx context.Context, away bool) {
	c := newMesh(t, ctx, 3)
	open := func(i int) {
		db, err := kvdb.New(c.nodes[i]).New(logging.Logger("secrets-test"), c.db, 1)
		require.NoError(t, err)
		t.Cleanup(func() { db.Close() })
		if i < len(c.dbs) {
			c.dbs[i] = db
			return
		}
		c.dbs = append(c.dbs, db)
		c.paths = append(c.paths, t.TempDir())
	}
	for i := range c.nodes {
		open(i)
	}

	r := Ref{Scope: Cloud("auth"), Name: "pepper"}
	older := bundleOf(t, map[Ref]string{r: "v1"})
	for i := range c.nodes {
		initOn(t, ctx, c, i, older)
	}
	newer, err := Sealed(r, []byte("v2"), time.Now().Add(time.Hour))
	require.NoError(t, err)
	initOn(t, ctx, c, 2, &secretsv2.InitBundle{Secrets: []*secretsv2.InitSecret{newer}})
	require.Eventually(t, func() bool {
		versions, err := (&recordStore{db: c.dbs[0]}).Versions(ctx, r)
		return err == nil && len(versions) == 2
	}, 30*time.Second, 100*time.Millisecond)
	if away {
		c.dbs[2].Close()
	}

	for i := range 2 {
		c.launch(t, c.nodes[i], c.dbs[i], c.paths[i])
	}
	consumer := c.client(t)

	// Without its owner nobody can open v2: it is kept, never served, and no
	// longer reported, and v1 is what everyone reads.
	require.Eventually(t, func() bool {
		c.reconcileAll(ctx)
		for _, srv := range c.live() {
			active, err := srv.ActiveKey(ctx)
			if err != nil || len(active.Members) != 2 || srv.reconcile(ctx) != nil || versionsOn(t, ctx, srv, r) != 2 {
				return false
			}
		}
		return true
	}, 60*time.Second, 200*time.Millisecond, "the survivors never settled without the owner")
	got, err := consumer.Get(ctx, r)
	require.NoError(t, err)
	assert.Equal(t, "v1", string(got))

	if away {
		open(2)
		// Its first look is stale: v2 only under its own one-member key, v1
		// under every node's. That must not prune v1.
		returning, err := New(c.nodes[2].Peer(), c.dbs[2], WithRoot(c.paths[2]))
		require.NoError(t, err)
		require.NoError(t, returning.prune(ctx))
		assert.Equal(t, 2, versionsOn(t, ctx, returning, r), "a stale view pruned the older version")
		require.NoError(t, returning.Close())
	}

	// The owner back, v2 is carried forward and, held as widely as v1, wins
	// on every node: the newest write is not lost.
	c.launch(t, c.nodes[2], c.dbs[2], c.paths[2])
	require.Eventually(t, func() bool {
		c.reconcileAll(ctx)
		for _, srv := range c.live() {
			active, err := srv.ActiveKey(ctx)
			if err != nil || len(active.Members) != 3 || srv.reconcile(ctx) != nil {
				return false
			}
			versions, err := srv.records.Versions(ctx, r)
			if err != nil || len(versions) != 1 || versions[0].CID != newer.GetCid() {
				return false
			}
		}
		return true
	}, 60*time.Second, 200*time.Millisecond, "the cluster never converged on the newest version")
	got, err = consumer.Get(ctx, r)
	require.NoError(t, err)
	assert.Equal(t, "v2", string(got))
}

// TestANewestVersionItsOwnerHasNotCarriedForwardIsKeptAndWins: the owner of
// the newest version is gone before carrying it forward, then returns.
func TestANewestVersionItsOwnerHasNotCarriedForwardIsKeptAndWins(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ownerAway(t, ctx, false)
}

// TestAnOwnerReturningWithAStaleStoreKeepsTheOlderVersion: the same, with the
// owner's database closed while it was away, so it returns on a stale view.
func TestAnOwnerReturningWithAStaleStoreKeepsTheOlderVersion(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ownerAway(t, ctx, true)
}
