package secrets

import (
	"context"
	"crypto/rand"
	"fmt"
	"slices"
	"testing"
	"time"

	logging "github.com/ipfs/go-log/v2"
	peerCore "github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	peerNode "github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/pkg/kvdb"
	"github.com/taubyte/tau/pkg/secrets/tdh"
)

// TestRepairIsBestEffortAndRetries pins repairShares' contract: a member that
// cannot replay makes one pass fail quietly, leaves the node a non-holder rather
// than inventing a share, and the next pass -- once the member is back --
// rebuilds the same share without moving the epoch.
func TestRepairIsBestEffortAndRetries(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := newCluster(t, ctx, 3)
	c.bootstrap(t, ctx, 0)
	key := c.awaitKey(t, ctx)
	require.NoError(t, c.client(t).Set(ctx, ref("repaired"), []byte("v")))

	damaged := c.services[2]
	before, err := damaged.shares.Get(ctx, key.KeyID)
	require.NoError(t, err)
	require.NoError(t, damaged.shares.Delete(ctx, key.KeyID))

	// A member of the key is down but still on the roster: replay from it fails.
	require.NoError(t, c.services[1].Close())
	damaged.repairShares(ctx)
	_, err = damaged.shares.Get(ctx, key.KeyID)
	require.Error(t, err, "repair must not persist a share it could not rebuild")

	c.restart(t, 1)
	require.Eventually(t, func() bool {
		damaged.repairShares(ctx)
		_, err := damaged.shares.Get(ctx, key.KeyID)
		return err == nil
	}, settleFor, 200*time.Millisecond, "repair never retried once the member was back")

	after, err := damaged.shares.Get(ctx, key.KeyID)
	require.NoError(t, err)
	assert.True(t, before.Value.IsEqual(after.Value), "repair rebuilt a different share")
	assert.Equal(t, key.KeyID, mustActive(t, ctx, damaged).KeyID, "repair moved the epoch")
}

// TestRepairLeavesKeysNothingIsWrappedUnder pins the guard that stops a node
// holding only its private key from pulling back shares of epochs the cluster
// has moved past: a key no secret uses and that is not active is never
// repaired, even with every member up and able to replay it.
func TestRepairLeavesKeysNothingIsWrappedUnder(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := newCluster(t, ctx, 2)
	c.bootstrap(t, ctx, 0)
	key := c.awaitKey(t, ctx)

	srv := c.services[1]
	require.NoError(t, srv.shares.Delete(ctx, key.KeyID))

	// With the share gone this node offers no key, and nothing is wrapped
	// under it yet, so from here the key is in use by nothing this node sees.
	inUse, err := srv.keysInUse(ctx)
	require.NoError(t, err)
	require.NotContains(t, inUse, key.KeyID)

	srv.repairShares(ctx)
	_, err = srv.shares.Get(ctx, key.KeyID)
	require.Error(t, err, "repair rebuilt a share no secret needs")

	// The moment a secret is wrapped under it, it is in use and repair acts.
	require.NoError(t, c.client(t).Set(ctx, ref("now-in-use"), []byte("v")))
	require.Eventually(t, func() bool {
		srv.repairShares(ctx)
		_, err := srv.shares.Get(ctx, key.KeyID)
		return err == nil
	}, settleFor, 200*time.Millisecond, "repair ignored a key a secret is wrapped under")
}

// TestWithoutStatedMembersTheRosterIsWhoSpeaksTheProtocol covers the default
// membership source: peers advertising the secrets protocol, and nobody else.
func TestWithoutStatedMembersTheRosterIsWhoSpeaksTheProtocol(t *testing.T) {
	ctx := t.Context()
	name := fmt.Sprintf("secrets-%d", clusters.Add(1))

	var services []*Service
	var nodes []peerNode.Node
	for range 2 {
		node := peerNode.Mock(ctx)
		t.Cleanup(func() { node.Close() })
		db, err := kvdb.New(node).New(logging.Logger("secrets-test"), name, 1)
		require.NoError(t, err)
		t.Cleanup(func() { db.Close() })
		srv, err := New(node.Peer(), db, WithRoot(t.TempDir()), WithBootstrapDelay(never))
		require.NoError(t, err)
		require.NoError(t, srv.Start(ctx))
		t.Cleanup(func() { srv.Close() })
		services, nodes = append(services, srv), append(nodes, node)
	}

	// A connected peer that does not run the service is not a member.
	bystander := peerNode.Mock(ctx)
	t.Cleanup(func() { bystander.Close() })

	require.NoError(t, peerNode.LinkAllPeers())
	for _, other := range []peerNode.Node{nodes[1], bystander} {
		require.NoError(t, nodes[0].Peer().Connect(ctx, other.Peer().Peerstore().PeerInfo(other.ID())))
	}

	want := []string{nodes[0].ID().String(), nodes[1].ID().String()}
	slices.Sort(want)
	require.Eventually(t, func() bool {
		members, err := services[0].roster(ctx)
		return err == nil && assert.ObjectsAreEqual(want, members)
	}, 60*time.Second, 100*time.Millisecond, "the roster never settled on the peers speaking the protocol")
}

// TestARebuiltShareIsCheckedBeforeItIsKept pins recoverShare's refusals. A
// share that does not match the published key must never reach the disk: it
// would look healthy there, so repair would never run again, and the node would
// poison every decryption it joined.
func TestARebuiltShareIsCheckedBeforeItIsKept(t *testing.T) {
	srv, client, _ := single(t)
	ctx := t.Context()
	require.NoError(t, client.Set(ctx, "p", "e", "n", []byte("v")))
	key := mustActive(t, ctx, srv)
	require.NoError(t, srv.shares.Delete(ctx, key.KeyID))

	other, err := tdh.Group.NewElement().MulGen(tdh.Group.RandomNonZeroScalar(rand.Reader)).MarshalBinaryCompress()
	require.NoError(t, err)

	tamper := func(change func(*KeyMeta)) *KeyMeta {
		copied := *key
		change(&copied)
		return &copied
	}
	for name, tc := range map[string]struct {
		key  *KeyMeta
		want string
	}{
		"not a member":       {tamper(func(k *KeyMeta) { k.Members = []string{"someone-else"} }), "not a member"},
		"no verification":    {tamper(func(k *KeyMeta) { k.Verification = nil }), "no verification vector"},
		"bad verification":   {tamper(func(k *KeyMeta) { k.Verification = [][]byte{[]byte("x")} }), "verification coefficient"},
		"another public key": {tamper(func(k *KeyMeta) { k.Public = other }), "does not belong"},
		"off the polynomial": {tamper(func(k *KeyMeta) { k.Verification = [][]byte{other} }), "group polynomial"},
	} {
		t.Run(name, func(t *testing.T) {
			require.ErrorContains(t, srv.recoverShare(ctx, tc.key), tc.want)
			_, err := srv.shares.Get(ctx, key.KeyID)
			require.Error(t, err, "a refused share was kept")
		})
	}

	// The untampered key rebuilds.
	require.NoError(t, srv.recoverShare(ctx, key))
	_, err = srv.shares.Get(ctx, key.KeyID)
	require.NoError(t, err)
}

func TestSetRefusesWhatItCannotStore(t *testing.T) {
	srv, client, _ := single(t)
	ctx := t.Context()
	require.NoError(t, client.Set(ctx, "p", "e", "n", []byte("v")))
	key := mustActive(t, ctx, srv)
	ref := Ref{Project: "p", Environment: "e", Name: "other"}

	require.ErrorContains(t, srv.Set(ctx, ref, []byte("c"), key.KeyID, nil), "missing wrapping")
	require.Error(t, srv.Set(ctx, ref, []byte("c"), "unknown-key", &tdh.Wrapping{}))
	require.ErrorIs(t, srv.Set(ctx, Ref{}, []byte("c"), key.KeyID, &tdh.Wrapping{}), ErrInvalidRef)

	_, err := srv.records.Get(ctx, ref)
	require.Error(t, err, "a refused Set left a record behind")
}

// TestAClientRefusesBadReferencesBeforeAskingAnyone: with nobody to ask, an
// invalid reference must still come back as ErrInvalidRef, which proves the
// check runs locally rather than costing a round trip per member.
func TestAClientRefusesBadReferencesBeforeAskingAnyone(t *testing.T) {
	node := peerNode.Mock(t.Context())
	t.Cleanup(func() { node.Close() })

	// Named as a member, but this node runs no service, so it is not a target.
	client, err := NewClient(node.Peer(), func() []peerCore.ID { return []peerCore.ID{node.ID()} }, func() *Service { return nil })
	require.NoError(t, err)
	ctx := t.Context()

	require.ErrorIs(t, client.Set(ctx, "p", "e", "", []byte("v")), ErrInvalidRef)
	_, err = client.Get(ctx, "p", "", "n")
	require.ErrorIs(t, err, ErrInvalidRef)
	_, err = client.Secret(ctx, "", "e", "n")
	require.ErrorIs(t, err, ErrInvalidRef)
	require.ErrorIs(t, client.Delete(ctx, "p", "e", "a/b"), ErrInvalidRef)

	_, err = client.Get(ctx, "p", "e", "n")
	require.ErrorIs(t, err, ErrNoMembers)
}

// TestReconciliationStepsReportAFailingStore: every step that walks the stored
// secrets must surface a store failure rather than conclude there is nothing to
// do, or a node with a broken store would quietly skip carrying secrets forward.
func TestReconciliationStepsReportAFailingStore(t *testing.T) {
	ctx := t.Context()
	db := broken{}
	srv := &Service{db: db, records: &recordStore{db: db}, wrappings: &wrappingStore{db: db}, keys: &keyStore{db: db}}

	_, err := srv.keysInUse(ctx)
	require.ErrorIs(t, err, errBroken)
	require.ErrorIs(t, srv.reEncrypt(ctx, &KeyMeta{}), errBroken)
	_, err = srv.supersededKeys(ctx, Ref{Project: "p", Environment: "e", Name: "n"}, &KeyMeta{})
	require.ErrorIs(t, err, errBroken)
}

// TestARoundMustNameItsEpochAndRoster: a round id is derived from its epoch and
// members, so a contribution claiming an id that does not match them is refused
// rather than joined under the wrong roster.
func TestARoundMustNameItsEpochAndRoster(t *testing.T) {
	srv, _, node := single(t)
	members := []string{node.ID().String()}

	_, err := srv.joinRound(t.Context(), roundID(2, members), 1, members)
	require.ErrorContains(t, err, "does not name its epoch and roster")

	r, err := srv.joinRound(t.Context(), roundID(1, members), 1, members)
	require.NoError(t, err)
	again, err := srv.joinRound(t.Context(), roundID(1, members), 1, members)
	require.NoError(t, err)
	assert.Same(t, r, again, "joining a round twice must return the same round")
}
