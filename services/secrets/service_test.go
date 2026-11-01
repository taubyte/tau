package secrets

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudflare/circl/secretsharing"
	logging "github.com/ipfs/go-log/v2"
	peerCore "github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreKvdb "github.com/taubyte/tau/core/kvdb"
	peerNode "github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/pkg/kvdb"
	"github.com/taubyte/tau/pkg/secrets/tdh"
)

// clusters numbers test clusters so each gets an isolated replication topic.
var clusters atomic.Uint64

// wantThreshold mirrors the service's policy: a majority, capped so the cluster
// can always lose one node and still reach it.
func wantThreshold(n int) uint {
	if majority := n/2 + 1; n > 1 && majority > n-1 {
		return uint(n - 1)
	} else {
		return uint(majority)
	}
}

// settleFor bounds how long a test waits for a write to reach every replica.
const settleFor = 30 * time.Second

// never keeps the boot timer out of the way: tests drive key generation
// explicitly, once the mesh is up and every node advertises the protocol.
const never = time.Hour

type cluster struct {
	// mu guards nodes, services and stopped against memberIDs, which every
	// running service reads from its own goroutines.
	mu sync.Mutex

	db       string
	opts     []Option
	nodes    []peerNode.Node
	consumer peerNode.Node // runs no service, the way a real client node does not
	services []*Service
	stopped  []int
	dbs      []coreKvdb.KVDB
	paths    []string
}

// newCluster brings up n meshed nodes, each running the secrets service over
// real streams.
//
// Every node gets its OWN replicating kvdb, not a shared handle. That is the
// difference between testing the design and testing a convenient fiction: the
// hazards this service is built around -- a node acting on a view that has not
// caught up yet -- only exist when replication is real and takes time.
func newCluster(t *testing.T, ctx context.Context, n int, opts ...Option) *cluster {
	t.Helper()

	c := newMesh(t, ctx, n, opts...)
	for _, node := range c.nodes {
		c.start(t, node)
	}

	c.awaitMesh(t, ctx)

	return c
}

// newMesh connects n nodes and a consumer, and starts no service on them.
func newMesh(t *testing.T, ctx context.Context, n int, opts ...Option) *cluster {
	t.Helper()

	// The mocknet is process-wide and kvdb replicates by database name, so two
	// clusters sharing a name would replicate into each other. Give each test
	// its own.
	c := &cluster{opts: opts, db: fmt.Sprintf("secrets-%d", clusters.Add(1))}
	for range n {
		node := peerNode.Mock(ctx)
		t.Cleanup(func() { node.Close() })
		c.nodes = append(c.nodes, node)
	}

	c.consumer = peerNode.Mock(ctx)
	t.Cleanup(func() { c.consumer.Close() })

	require.NoError(t, peerNode.LinkAllPeers())
	mesh := append(c.nodes, c.consumer)
	for i, node := range mesh {
		for _, other := range mesh[i+1:] {
			require.NoError(t, node.Peer().Connect(ctx, other.Peer().Peerstore().PeerInfo(other.ID())))
		}
	}

	return c
}

// awaitMesh waits until every node can see the whole cluster. Sleeping a fixed
// interval instead makes the suite fail under load for a reason that has
// nothing to do with the code: a node that bootstraps before identify has
// finished forms a key over a roster that is merely incomplete.
func (c *cluster) awaitMesh(t *testing.T, ctx context.Context) {
	t.Helper()

	require.Eventually(t, func() bool {
		for _, srv := range c.live() {
			if len(srv.roster()) != len(c.nodes) {
				return false
			}
		}

		return true
	}, 60*time.Second, 100*time.Millisecond, "the cluster never saw all of its own nodes")
}

// start brings up the service on one node, with its own replicating database
// and its own share store. The service opens its own stream service on its own
// protocol, so there is nothing for a host to wire in.
func (c *cluster) start(t *testing.T, node peerNode.Node) int {
	t.Helper()

	db, err := kvdb.New(node).New(logging.Logger("secrets-test"), c.db, 1)
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	path := t.TempDir()
	c.launch(t, node, db, path)

	c.dbs = append(c.dbs, db)
	c.paths = append(c.paths, path)

	return len(c.services) - 1
}

// launch starts one service instance over an existing node, database and share
// store, so a node can be restarted onto the state it already had.
func (c *cluster) launch(t *testing.T, node peerNode.Node, db coreKvdb.KVDB, path string) *Service {
	t.Helper()

	srv := c.serve(t, node, db, path)
	c.mu.Lock()
	c.services = append(c.services, srv)
	c.mu.Unlock()

	return srv
}

// serve builds and starts one service over a node, a database and a share
// store, announcing epochs on the node's own gossip.
func (c *cluster) serve(t *testing.T, node peerNode.Node, db coreKvdb.KVDB, path string) *Service {
	t.Helper()

	// Membership is stated, as discovery states it on a node, rather than read off
	// identify, whose record of a late-listening service arrives only by a push.
	opts := append([]Option{WithRoot(path), WithPubSub(node.Messaging()), WithBootstrapDelay(never), WithMembers(c.memberIDs), WithReplacedRetention(0)}, c.opts...)
	srv, err := New(node.Peer(), db, opts...)
	require.NoError(t, err)
	require.NoError(t, srv.Start(context.Background()))
	t.Cleanup(func() { srv.Close() })

	return srv
}

// grow adds a node to the cluster after it has already formed a key, which is
// what makes the roster change and therefore what triggers rotation.
func (c *cluster) grow(t *testing.T, ctx context.Context) {
	t.Helper()

	node := peerNode.Mock(ctx)
	t.Cleanup(func() { node.Close() })

	require.NoError(t, peerNode.LinkAllPeers())
	for _, other := range append(c.nodes, c.consumer) {
		require.NoError(t, node.Peer().Connect(ctx, other.Peer().Peerstore().PeerInfo(other.ID())))
	}

	c.mu.Lock()
	c.nodes = append(c.nodes, node)
	c.mu.Unlock()
	c.start(t, node)

	c.awaitMesh(t, ctx)
}

// reconcileAll runs one reconciliation pass on every live node, the way each
// node's own loop would.
func (c *cluster) reconcileAll(ctx context.Context) {
	for _, srv := range c.live() {
		srv.reconcile(ctx)
	}
}

// restart tears a node's service down and brings it back on the same database
// and the same share store, the way a process restart does.
func (c *cluster) restart(t *testing.T, i int) {
	t.Helper()

	require.NoError(t, c.services[i].Close())

	srv := c.serve(t, c.nodes[i], c.dbs[i], c.paths[i])
	c.mu.Lock()
	c.services[i] = srv
	c.mu.Unlock()
}

// stop takes a node out of service for good, the way a lost machine does. The
// service stops advertising the protocol, so the survivors' discovery no longer
// counts it as a member.
func (c *cluster) stop(t *testing.T, i int) {
	t.Helper()

	require.NoError(t, c.services[i].Close())
	c.mu.Lock()
	c.stopped = append(c.stopped, i)
	c.mu.Unlock()
}

func (c *cluster) live() []*Service {
	live := make([]*Service, 0, len(c.services))
	for i, srv := range c.services {
		if !slices.Contains(c.stopped, i) {
			live = append(live, srv)
		}
	}

	return live
}

// bootstrap runs the key generation the boot timer would have run. Passing more
// than one node starts them at the same instant, which is what a cluster
// booting together does.
func (c *cluster) bootstrap(t *testing.T, ctx context.Context, nodes ...int) {
	t.Helper()

	var wg sync.WaitGroup
	for _, i := range nodes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			assert.NoError(t, c.services[i].ensureKey(ctx))
		}()
	}
	wg.Wait()
}

// client talks to the cluster the way a real consumer would.
// testNamespace scopes every secret these tests write, so a List assertion
// means "the secrets this test wrote" rather than "everything on the node".
const (
	testNamespace   = "test"
	testEnvironment = "env"
)

func ref(name string) Ref {
	return Ref{Scope: Project(testNamespace, testEnvironment), Name: name}
}

// newest is the newest version of ref a node stores, or one naming nothing.
func newest(ctx context.Context, srv *Service, ref Ref) version {
	versions, err := srv.records.Versions(ctx, ref)
	if err != nil || len(versions) == 0 {
		return version{Ref: ref, CID: "none"}
	}
	return versions[0].version
}

// refClient is the Client with List answering references, which is how these
// tests read.
type refClient struct{ *Client }

// List lists one project's test environment as references.
func (r refClient) List(ctx context.Context, project string) ([]Ref, error) {
	names, err := r.Client.List(ctx, Project(project, testEnvironment))
	if err != nil {
		return nil, err
	}
	refs := make([]Ref, 0, len(names))
	for _, name := range names {
		refs = append(refs, Ref{Scope: Project(project, testEnvironment), Name: name})
	}
	return refs, nil
}

// client talks to the cluster from a node that runs no service, the way a
// real consumer does: it is told who the members are and runs none itself.
func (c *cluster) client(t *testing.T) refClient {
	t.Helper()

	secrets, err := NewClient(c.consumer.Peer(), c.memberIDs, nil)
	require.NoError(t, err)
	t.Cleanup(func() { secrets.Close() })

	return refClient{secrets}
}

// memberIDs are the nodes whose service is running.
func (c *cluster) memberIDs() []peerCore.ID {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []peerCore.ID
	for i, node := range c.nodes {
		if i < len(c.services) && !slices.Contains(c.stopped, i) {
			out = append(out, node.ID())
		}
	}
	return out
}

// awaitKey waits for every node to agree on the active key and to hold its
// share of it.
func (c *cluster) awaitKey(t *testing.T, ctx context.Context) *KeyMeta {
	t.Helper()

	var key *KeyMeta
	require.Eventually(t, func() bool {
		key = nil
		for _, srv := range c.services {
			active, err := srv.ActiveKey(ctx)
			if err != nil {
				return false
			}
			if key == nil {
				key = active
			}
			if active.KeyID != key.KeyID {
				return false
			}
			if _, err := srv.shares.Get(ctx, active.KeyID); err != nil {
				return false
			}
		}
		return key != nil
	}, 30*time.Second, 50*time.Millisecond, "cluster did not settle on a usable key")

	return key
}

func TestSingleNode(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := newCluster(t, ctx, 1)
	c.bootstrap(t, ctx, 0)
	key := c.awaitKey(t, ctx)

	// One node runs the same scheme as twenty, at threshold 1.
	assert.Equal(t, uint(1), key.Threshold)
	assert.Len(t, key.Members, 1)

	secrets := c.client(t)
	require.NoError(t, secrets.Set(ctx, ref("owner-key"), []byte("single node secret")))

	got, err := secrets.Get(ctx, ref("owner-key"))
	require.NoError(t, err)
	assert.Equal(t, []byte("single node secret"), got)
}

func TestThresholdAcrossNodes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const n = 3
	c := newCluster(t, ctx, n)
	c.bootstrap(t, ctx, 0)
	key := c.awaitKey(t, ctx)

	require.Len(t, key.Members, n)
	require.Equal(t, uint(2), key.Threshold)

	secrets := c.client(t)
	plaintext := []byte("no single node can read this")
	require.NoError(t, secrets.Set(ctx, ref("owner-key"), plaintext))

	got, err := secrets.Get(ctx, ref("owner-key"))
	require.NoError(t, err)
	assert.Equal(t, plaintext, got)

	// A single node's partial is not enough on its own.
	_, _, wrapping, writer, err := c.services[0].Get(ctx, ref("owner-key"))
	require.NoError(t, err)

	partial, err := c.services[0].Partial(ctx, newest(ctx, c.services[0], ref("owner-key")), key.KeyID, writer)
	require.NoError(t, err)

	public, err := key.PublicKey()
	require.NoError(t, err)

	_, err = tdh.Unwrap(wrapping, public, []*tdh.Partial{partial}, key.Threshold, nil)
	require.ErrorContains(t, err, "need 2")
}

func TestConcurrentBootstrapAgreesOnOneKey(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Every node starts a round at once. A round is named by its roster, so
	// these are all the same round rather than three competing ones.
	c := newCluster(t, ctx, 3)
	c.bootstrap(t, ctx, 0, 1, 2)
	key := c.awaitKey(t, ctx)

	require.Len(t, key.Members, 3)

	keys, err := c.services[0].keys.List(ctx)
	require.NoError(t, err)
	assert.Len(t, keys, 1, "concurrent bootstrap published more than one key")

	// Every member publishes the record, so it must be byte-identical from all
	// of them -- otherwise it is a key two nodes write differently.
	for _, srv := range c.services {
		stored, err := srv.keys.Get(ctx, key.KeyID)
		require.NoError(t, err)
		assert.Equal(t, key, stored)
	}
}

func TestSecretLifecycle(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := newCluster(t, ctx, 2)
	c.bootstrap(t, ctx, 0)
	c.awaitKey(t, ctx)
	secrets := c.client(t)

	// Absence is reported as ErrNotFound, distinguishable from "cannot reach a
	// threshold right now" -- which is what makes it safe to act on. It takes a
	// settle window to establish, and the caller's own deadline shortens that.
	quick, cancelQuick := context.WithTimeout(ctx, 2*time.Second)
	defer cancelQuick()

	_, err := secrets.Get(quick, ref("absent"))
	require.ErrorIs(t, err, ErrNotSet)

	require.NoError(t, secrets.Set(ctx, ref("first"), []byte("one")))
	require.NoError(t, secrets.Set(ctx, ref("second"), []byte("two")))

	// Reads land on whichever replica answers, so listing and existence are
	// eventually consistent with a write made through another one. Asserting
	// that honestly is the point: pretending otherwise would only make the test
	// pass on a machine fast enough to hide it.
	require.Eventually(t, func() bool {
		ids, err := secrets.List(ctx, testNamespace)

		return err == nil && len(ids) == 2
	}, settleFor, 100*time.Millisecond, "writes did not converge across replicas")

	got, err := secrets.Get(ctx, ref("first"))
	require.NoError(t, err)
	assert.Equal(t, []byte("one"), got)

	require.NoError(t, secrets.Delete(ctx, ref("first")))

	require.Eventually(t, func() bool {
		probe, cancelProbe := context.WithTimeout(ctx, 2*time.Second)
		defer cancelProbe()

		_, err := secrets.Get(probe, ref("first"))

		return errors.Is(err, ErrNotSet)
	}, settleFor, 100*time.Millisecond, "the delete did not converge across replicas")

	// Deleting one secret must not disturb another.
	got, err = secrets.Get(ctx, ref("second"))
	require.NoError(t, err)
	assert.Equal(t, []byte("two"), got)
}

func TestRejectsUnusableReferences(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := newCluster(t, ctx, 1)
	c.bootstrap(t, ctx, 0)
	c.awaitKey(t, ctx)
	secrets := c.client(t)

	// Either part carrying a separator would land outside its prefix and
	// truncate back to something else on the way out. Both parts are checked:
	// a namespace is as much a path segment as a name.
	for label, bad := range map[string]Ref{
		"empty name":          {Project: testNamespace, Environment: testEnvironment, Name: ""},
		"empty namespace":     {Project: "", Environment: testEnvironment, Name: "owner-key"},
		"separator in name":   {Project: testNamespace, Environment: testEnvironment, Name: "project/owner-key"},
		"separator in ns":     {Project: "a/b", Environment: testEnvironment, Name: "owner-key"},
		"traversal in name":   {Project: testNamespace, Environment: testEnvironment, Name: "../keys/other"},
		"traversal in ns":     {Project: "..", Environment: testEnvironment, Name: "owner-key"},
		"dot-dot name":        {Project: testNamespace, Environment: testEnvironment, Name: ".."},
		"dot name":            {Project: testNamespace, Environment: testEnvironment, Name: "."},
		"whitespace in name":  {Project: testNamespace, Environment: testEnvironment, Name: "owner key"},
		"oversized name":      {Project: testNamespace, Environment: testEnvironment, Name: strings.Repeat("x", 129)},
		"unprintable in name": {Project: testNamespace, Environment: testEnvironment, Name: "owner\x00key"},
		"empty environment":   {Project: testNamespace, Environment: "", Name: "owner-key"},
		"separator in env":    {Project: testNamespace, Environment: "a/b", Name: "owner-key"},
	} {
		t.Run(label, func(t *testing.T) {
			require.ErrorIs(t, secrets.Set(ctx, bad, []byte("x")), ErrInvalidRef)
			_, err := secrets.Get(ctx, bad)
			require.ErrorIs(t, err, ErrInvalidRef)
			require.ErrorIs(t, secrets.Delete(ctx, bad), ErrInvalidRef)
		})
	}
}

func TestNamespacesIsolateNames(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := newCluster(t, ctx, 1)
	c.bootstrap(t, ctx, 0)
	c.awaitKey(t, ctx)
	secrets := c.client(t)

	one := Ref{Scope: Project("shop", testEnvironment), Name: "db-password"}
	two := Ref{Scope: Project("blog", testEnvironment), Name: "db-password"}

	require.NoError(t, secrets.Set(ctx, one, []byte("a's password")))
	require.NoError(t, secrets.Set(ctx, two, []byte("b's password")))

	got, err := secrets.Get(ctx, one)
	require.NoError(t, err)
	assert.Equal(t, []byte("a's password"), got)

	got, err = secrets.Get(ctx, two)
	require.NoError(t, err)
	assert.Equal(t, []byte("b's password"), got)

	// Listing is scoped to one project environment.
	listed, err := secrets.List(ctx, "shop")
	require.NoError(t, err)
	assert.Equal(t, []Ref{one}, listed)

	all, err := c.services[0].records.All(ctx)
	require.NoError(t, err)
	assert.Len(t, all, 2)

	// Deleting one leaves the other, even though they share a name.
	require.NoError(t, secrets.Delete(ctx, one))

	got, err = secrets.Get(ctx, two)
	require.NoError(t, err)
	assert.Equal(t, []byte("b's password"), got)
}

func TestRotationCarriesSecretsForward(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := newCluster(t, ctx, 2)
	c.bootstrap(t, ctx, 0)
	first := c.awaitKey(t, ctx)
	require.Len(t, first.Members, 2)

	secrets := c.client(t)
	plaintext := []byte("survives rotation")
	require.NoError(t, secrets.Set(ctx, ref("owner-key"), plaintext))

	// A node joining changes the roster, which is a rotation trigger.
	c.grow(t, ctx)

	var second *KeyMeta
	require.Eventually(t, func() bool {
		c.reconcileAll(ctx)

		active, err := c.services[0].ActiveKey(ctx)
		if err != nil || active.Epoch == first.Epoch {
			return false
		}
		second = active

		// Carried forward means a wrapping exists under the new epoch.
		_, _, err = c.services[0].wrappings.Get(ctx, newest(ctx, c.services[0], ref("owner-key")), second.KeyID)

		return err == nil
	}, 60*time.Second, 200*time.Millisecond, "the secret was not carried into a new epoch")

	assert.Greater(t, second.Epoch, first.Epoch)
	assert.NotEqual(t, first.KeyID, second.KeyID, "rotation must regenerate, not reshare")
	assert.Len(t, second.Members, 3)

	// The plaintext is unchanged and readable under the new epoch.
	got, err := secrets.Get(ctx, ref("owner-key"))
	require.NoError(t, err)
	assert.Equal(t, plaintext, got)

	// Once every secret is carried forward, the superseded epoch is retired.
	require.Eventually(t, func() bool {
		c.reconcileAll(ctx)

		keyIDs, err := c.services[0].wrappings.KeyIDs(ctx, newest(ctx, c.services[0], ref("owner-key")))

		return err == nil && len(keyIDs) == 1 && keyIDs[0] == second.KeyID
	}, 60*time.Second, 200*time.Millisecond, "the superseded epoch was not retired")

	// Retirement collects wrappings, never key material: it is per-secret and
	// local, so it cannot know whether some node still needs the old key.
	//
	// The count is a lower bound, not an equality. A node that joins before its
	// store has replicated cannot tell "no key exists" from "I have not caught
	// up", so it may start a redundant round -- two keys in one epoch. That is
	// wasteful, not wrong: both are valid, both are readable, and a later
	// rotation over a settled roster collapses it.
	keys, err := c.services[0].keys.List(ctx)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(keys), 2, "retirement destroyed key material")

	byID := map[string]bool{}
	for _, k := range keys {
		byID[k.KeyID] = true
	}
	assert.True(t, byID[first.KeyID], "the superseded key was destroyed")
	assert.True(t, byID[second.KeyID], "the active key is missing")

	got, err = secrets.Get(ctx, ref("owner-key"))
	require.NoError(t, err)
	assert.Equal(t, plaintext, got)
}

func TestNodeOffersOnlyKeysItHolds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := newCluster(t, ctx, 1)
	c.bootstrap(t, ctx, 0)
	active := c.awaitKey(t, ctx)

	// A newer key this node has no share of must not be offered: the node would
	// be advertising material it cannot serve a partial for. No tally of other
	// members is consulted -- only whether this node holds it.
	foreign := &KeyMeta{
		KeyID:     "later-epoch-this-node-does-not-hold",
		Epoch:     active.Epoch + 1,
		Public:    active.Public,
		Threshold: 2,
		Members:   []string{"a", "b"},
	}
	require.NoError(t, c.services[0].keys.Put(ctx, foreign))

	got, err := c.services[0].ActiveKey(ctx)
	require.NoError(t, err)
	assert.Equal(t, active.KeyID, got.KeyID, "a node offered a key it holds no share of")

	// It still counts for numbering, so a rotation lands past it rather than
	// colliding with it.
	epoch, err := c.services[0].keys.Epoch(ctx)
	require.NoError(t, err)
	assert.Equal(t, foreign.Epoch, epoch)
}

func TestRetirementNeedsNoAgreement(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := newCluster(t, ctx, 2)
	c.bootstrap(t, ctx, 0)
	first := c.awaitKey(t, ctx)

	secrets := c.client(t)
	plaintext := []byte("must outlive the old epoch")
	require.NoError(t, secrets.Set(ctx, ref("owner-key"), plaintext))

	c.grow(t, ctx)

	// Only ONE node ever reconciles. It carries the secret forward and collects
	// the superseded wrapping without asking anybody.
	var second *KeyMeta
	require.Eventually(t, func() bool {
		c.services[0].reconcile(ctx)

		active, err := c.services[0].ActiveKey(ctx)
		if err != nil || active.Epoch == first.Epoch {
			return false
		}
		second = active

		keyIDs, err := c.services[0].wrappings.KeyIDs(ctx, newest(ctx, c.services[0], ref("owner-key")))

		return err == nil && len(keyIDs) == 1 && keyIDs[0] == second.KeyID
	}, 60*time.Second, 200*time.Millisecond, "a lone node could not retire a superseded wrapping")

	// Key material survives retirement: deciding nobody needs it any more would
	// take the cluster-wide agreement this design does not have.
	require.NoError(t, func() error {
		_, err := c.services[0].keys.Get(ctx, first.KeyID)

		return err
	}(), "retirement destroyed a key record")

	_, err := c.services[0].shares.Get(ctx, first.KeyID)
	require.NoError(t, err, "retirement destroyed a share")

	got, err := secrets.Get(ctx, ref("owner-key"))
	require.NoError(t, err)
	assert.Equal(t, plaintext, got)
}

func TestSurvivesRestart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := newCluster(t, ctx, 3)
	c.bootstrap(t, ctx, 0)
	key := c.awaitKey(t, ctx)

	secrets := c.client(t)
	plaintext := []byte("must survive a restart")
	require.NoError(t, secrets.Set(ctx, ref("owner-key"), plaintext))

	got, err := secrets.Get(ctx, ref("owner-key"))
	require.NoError(t, err)
	require.Equal(t, plaintext, got)

	// Restart every node, one at a time.
	for i := range c.services {
		c.restart(t, i)
	}

	for i, srv := range c.services {
		active, err := srv.ActiveKey(ctx)
		require.NoErrorf(t, err, "node %d came back without a key", i)
		assert.Equalf(t, key.KeyID, active.KeyID, "node %d came back on a different key", i)

		_, _, _, writer, err := srv.Get(ctx, ref("owner-key"))
		require.NoErrorf(t, err, "node %d cannot read the record after restarting", i)

		_, err = srv.Partial(ctx, newest(ctx, srv, ref("owner-key")), key.KeyID, writer)
		require.NoErrorf(t, err, "node %d cannot serve partials after restarting", i)
	}

	got, err = secrets.Get(ctx, ref("owner-key"))
	require.NoError(t, err)
	assert.Equal(t, plaintext, got)
}

func TestSurvivesLosingANode(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := newCluster(t, ctx, 3)
	c.bootstrap(t, ctx, 0)
	key := c.awaitKey(t, ctx)
	require.Equal(t, uint(2), key.Threshold)

	secrets := c.client(t)
	plaintext := []byte("outlives a lost machine")
	require.NoError(t, secrets.Set(ctx, ref("owner-key"), plaintext))

	got, err := secrets.Get(ctx, ref("owner-key"))
	require.NoError(t, err)
	require.Equal(t, plaintext, got)

	// Lose one of three. Two holders remain, which is the threshold.
	c.stop(t, 2)

	got, err = secrets.Get(ctx, ref("owner-key"))
	require.NoError(t, err, "losing one node of three broke a 2-of-3 secret")
	assert.Equal(t, plaintext, got)

	// Lose a second. The threshold is gone and the secret must fail closed --
	// unreadable, not silently wrong.
	c.stop(t, 1)

	_, err = secrets.Get(ctx, ref("owner-key"))
	require.Error(t, err, "a secret was readable below its threshold")
}

func TestReconcileLoopRunsOnItsOwn(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := newCluster(t, ctx, 2, WithReconcileInterval(500*time.Millisecond))
	c.bootstrap(t, ctx, 0)
	first := c.awaitKey(t, ctx)

	secrets := c.client(t)
	plaintext := []byte("carried forward by the loop alone")
	require.NoError(t, secrets.Set(ctx, ref("owner-key"), plaintext))

	// Changing the roster is the trigger; from here nothing in the test drives
	// rotation, re-encryption or retirement.
	c.grow(t, ctx)

	require.Eventually(t, func() bool {
		active, err := c.services[0].ActiveKey(ctx)
		if err != nil || active.Epoch == first.Epoch {
			return false
		}

		keyIDs, err := c.services[0].wrappings.KeyIDs(ctx, newest(ctx, c.services[0], ref("owner-key")))

		return err == nil && len(keyIDs) == 1 && keyIDs[0] == active.KeyID
	}, 90*time.Second, 250*time.Millisecond, "the reconcile loop did not rotate and carry forward on its own")

	got, err := secrets.Get(ctx, ref("owner-key"))
	require.NoError(t, err)
	assert.Equal(t, plaintext, got)
}

func TestConcurrentReconciliation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := newCluster(t, ctx, 3)
	c.bootstrap(t, ctx, 0)
	first := c.awaitKey(t, ctx)

	secrets := c.client(t)
	for _, id := range []string{"alpha", "beta", "gamma"} {
		require.NoError(t, secrets.Set(ctx, ref(id), []byte("value of "+id)))
	}

	c.grow(t, ctx)

	require.Eventually(t, func() bool {
		var wg sync.WaitGroup
		for _, srv := range c.services {
			wg.Add(1)
			go func() {
				defer wg.Done()
				srv.reconcile(ctx)
			}()
		}
		wg.Wait()

		active, err := c.services[0].ActiveKey(ctx)
		if err != nil || active.Epoch == first.Epoch {
			return false
		}

		for _, id := range []string{"alpha", "beta", "gamma"} {
			if _, _, err := c.services[0].wrappings.Get(ctx, newest(ctx, c.services[0], ref(id)), active.KeyID); err != nil {
				return false
			}
		}

		return true
	}, 90*time.Second, 250*time.Millisecond, "concurrent reconciliation did not converge")

	// Concurrent reconciliation must not run the epoch counter away: nodes
	// deciding to rotate together derive the same next epoch and join one
	// round, so the cluster advances by one epoch, not one per node.
	keys, err := c.services[0].keys.Newest(ctx)
	require.NoError(t, err)
	epochs := map[uint64]struct{}{}
	for _, key := range keys {
		epochs[key.Epoch] = struct{}{}
		t.Logf("key %s epoch %d members %v", key.KeyID, key.Epoch, key.Members)
	}
	assert.LessOrEqualf(t, len(epochs), 2,
		"concurrent reconciliation advanced %d epochs for one roster change", len(epochs))

	for _, id := range []string{"alpha", "beta", "gamma"} {
		got, err := secrets.Get(ctx, ref(id))
		require.NoErrorf(t, err, "%s was lost", id)
		assert.Equal(t, []byte("value of "+id), got)
	}
}

func TestGrowsOneNodeAtATime(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := newCluster(t, ctx, 1)
	c.bootstrap(t, ctx, 0)

	first := c.awaitKey(t, ctx)
	require.Len(t, first.Members, 1)
	require.Equal(t, uint(1), first.Threshold, "a lone node runs the same scheme at threshold 1")

	secrets := c.client(t)

	// Everything written so far, and the size the cluster was when it was
	// written, so nothing is allowed to quietly become unreadable later.
	written := map[string][]byte{}
	store := func(t *testing.T, size int) {
		t.Helper()

		id := fmt.Sprintf("secret-at-%d-nodes", size)
		value := fmt.Appendf(nil, "written when the cluster had %d nodes", size)
		require.NoError(t, secrets.Set(ctx, ref(id), value))
		written[id] = value
	}

	readAll := func(t *testing.T, size int) {
		t.Helper()

		for id, want := range written {
			got, err := secrets.Get(ctx, ref(id))
			require.NoErrorf(t, err, "%s became unreadable at %d nodes", id, size)
			assert.Equalf(t, want, got, "%s changed at %d nodes", id, size)
		}
	}

	store(t, 1)
	readAll(t, 1)

	// Deliberately not subtests: grow registers cleanups, and on a subtest's t
	// those fire when the subtest ends -- tearing down nodes the rest of the
	// walk still needs.
	for size := 2; size <= 4; size++ {
		func() {
			c.grow(t, ctx)

			// Let the cluster settle onto a key over the new roster.
			var active *KeyMeta
			require.Eventually(t, func() bool {
				c.reconcileAll(ctx)

				key, err := c.services[0].ActiveKey(ctx)
				if err != nil || len(key.Members) != size {
					return false
				}
				active = key

				// Every secret carried forward onto it.
				for id := range written {
					if _, _, err := c.services[0].wrappings.Get(ctx, newest(ctx, c.services[0], ref(id)), active.KeyID); err != nil {
						return false
					}
				}

				return true
			}, 90*time.Second, 250*time.Millisecond, "the cluster did not settle at %d nodes", size)

			// The threshold follows the roster, capped so one node can always
			// be lost.
			assert.Equalf(t, wantThreshold(size), active.Threshold,
				"threshold did not track a roster of %d", size)
			assert.LessOrEqualf(t, int(active.Threshold), size-1,
				"a roster of %d cannot survive losing a node", size)

			// Every member must be able to contribute, or the threshold the key
			// advertises is a fiction.
			holders := 0
			for _, srv := range c.services {
				if _, err := srv.shares.Get(ctx, active.KeyID); err == nil {
					holders++
				}
			}
			assert.Equalf(t, size, holders, "only %d of %d members hold the key", holders, size)

			// Older secrets survive the growth, and new ones can be written.
			readAll(t, size)
			store(t, size)
			readAll(t, size)
		}()
	}

	// Four nodes in, everything ever written is still readable.
	assert.Len(t, written, 4)
	readAll(t, 4)
}

func TestShrinksOneNodeAtATime(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := newCluster(t, ctx, 4)
	c.bootstrap(t, ctx, 0)

	start := c.awaitKey(t, ctx)
	require.Len(t, start.Members, 4)

	secrets := c.client(t)
	written := map[string][]byte{}
	for _, id := range []string{"alpha", "beta"} {
		value := []byte("value of " + id)
		require.NoError(t, secrets.Set(ctx, ref(id), value))
		written[id] = value
	}

	readAll := func(t *testing.T, size int) {
		t.Helper()

		for id, want := range written {
			got, err := secrets.Get(ctx, ref(id))
			require.NoErrorf(t, err, "%s became unreadable at %d nodes", id, size)
			assert.Equalf(t, want, got, "%s changed at %d nodes", id, size)
		}
	}

	readAll(t, 4)

	// Remove the last node each time and let the survivors rotate down.
	for size := 3; size >= 1; size-- {
		c.stop(t, size)

		var active *KeyMeta
		require.Eventuallyf(t, func() bool {
			c.reconcileAll(ctx)

			key, err := c.live()[0].ActiveKey(ctx)
			if err != nil || len(key.Members) != size {
				return false
			}
			active = key

			for id := range written {
				if _, _, err := c.live()[0].wrappings.Get(ctx, newest(ctx, c.live()[0], ref(id)), active.KeyID); err != nil {
					return false
				}
			}

			return true
		}, 120*time.Second, 250*time.Millisecond, "the cluster did not rotate down to %d nodes", size)

		assert.Equalf(t, wantThreshold(size), active.Threshold, "threshold did not track a roster of %d", size)

		// Every surviving member holds the new key, and the node that left is
		// not in it.
		for i, srv := range c.live() {
			_, err := srv.shares.Get(ctx, active.KeyID)
			require.NoErrorf(t, err, "survivor %d does not hold the key at %d nodes", i, size)
		}
		assert.NotContainsf(t, active.Members, c.nodes[size].ID().String(),
			"the removed node is still in the roster at %d nodes", size)

		readAll(t, size)
	}
}

func TestShrinksToASingleNode(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := newCluster(t, ctx, 2)
	c.bootstrap(t, ctx, 0)

	key := c.awaitKey(t, ctx)
	require.Len(t, key.Members, 2)
	require.Equal(t, uint(1), key.Threshold, "two nodes must survive losing one")

	secrets := c.client(t)
	plaintext := []byte("written while both were up")
	require.NoError(t, secrets.Set(ctx, ref("owner-key"), plaintext))

	got, err := secrets.Get(ctx, ref("owner-key"))
	require.NoError(t, err)
	require.Equal(t, plaintext, got)

	c.stop(t, 1)

	// The survivor rotates onto a roster of itself and carries the secret over,
	// unattended.
	require.Eventually(t, func() bool {
		c.reconcileAll(ctx)

		active, err := c.live()[0].ActiveKey(ctx)
		if err != nil || len(active.Members) != 1 {
			return false
		}

		_, _, err = c.live()[0].wrappings.Get(ctx, newest(ctx, c.live()[0], ref("owner-key")), active.KeyID)

		return err == nil
	}, 120*time.Second, 250*time.Millisecond, "the survivor did not carry the secret forward on its own")

	got, err = secrets.Get(ctx, ref("owner-key"))
	require.NoError(t, err, "a secret was lost when a two-node cluster lost a node")
	assert.Equal(t, plaintext, got)
}

func mustActive(t *testing.T, ctx context.Context, srv *Service) *KeyMeta {
	t.Helper()

	key, err := srv.ActiveKey(ctx)
	require.NoError(t, err)

	return key
}

func TestRetirementProvesTheReplacementOpens(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := newCluster(t, ctx, 2)
	c.bootstrap(t, ctx, 0)
	real := c.awaitKey(t, ctx)

	secrets := c.client(t)
	plaintext := []byte("must outlive a bad replacement")
	require.NoError(t, secrets.Set(ctx, ref("owner-key"), plaintext))

	srv := c.services[0]

	// The write went through whichever node answered, so wait for this one to
	// have it before staging anything -- otherwise the assertion below races
	// replication rather than testing retirement.
	require.Eventually(t, func() bool {
		_, _, err := srv.wrappings.Get(ctx, newest(ctx, srv, ref("owner-key")), real.KeyID)

		return err == nil
	}, settleFor, 100*time.Millisecond, "the wrapping never reached this node")

	// A key nobody holds a share of, at a newer epoch so it sorts first.
	orphan := &KeyMeta{
		KeyID:     "orphan-key-nobody-holds",
		Epoch:     real.Epoch + 1,
		Public:    real.Public,
		Threshold: 1,
		Members:   real.Members,
	}
	require.NoError(t, srv.keys.Put(ctx, orphan))

	// A syntactically valid wrapping under it, which no quorum can open.
	public, err := orphan.PublicKey()
	require.NoError(t, err)
	dek := make([]byte, 32)
	unopenable, err := tdh.Wrap(public, dek, wrapAAD(newest(ctx, srv, ref("owner-key")), orphan.KeyID))
	require.NoError(t, err)
	require.NoError(t, srv.wrappings.Put(ctx, newest(ctx, srv, ref("owner-key")), orphan.KeyID, "someone", unopenable))

	// Retire against the orphan, which is exactly the state a rotation reaches
	// when fewer than t members persisted a share.
	require.NoError(t, srv.retire(ctx, orphan))

	keyIDs, err := srv.wrappings.KeyIDs(ctx, newest(ctx, srv, ref("owner-key")))
	require.NoError(t, err)
	assert.Contains(t, keyIDs, real.KeyID,
		"retirement deleted the only readable wrapping for one that cannot be opened")

	got, err := secrets.Get(ctx, ref("owner-key"))
	require.NoError(t, err, "the secret was destroyed by retirement")
	assert.Equal(t, plaintext, got)
}

func TestRebuildsALostShareWithoutRotating(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := newCluster(t, ctx, 3)
	c.bootstrap(t, ctx, 0)
	key := c.awaitKey(t, ctx)

	secrets := c.client(t)
	plaintext := []byte("survives a lost share")
	require.NoError(t, secrets.Set(ctx, ref("owner-key"), plaintext))

	damaged := c.services[2]
	before, err := damaged.shares.Get(ctx, key.KeyID)
	require.NoError(t, err)

	// Destroy it the way a changed at-rest key does: the entry is there and
	// cannot be opened.
	require.NoError(t, damaged.shares.Delete(ctx, key.KeyID))
	_, err = damaged.shares.Get(ctx, key.KeyID)
	require.Error(t, err, "the share should be unreadable now")

	_, err = damaged.ActiveKey(ctx)
	require.Error(t, err, "a node with no usable share must not offer a key")

	require.NoError(t, damaged.recoverShare(ctx, key))

	// Same share, not merely a working one: a different share would mean the
	// group key changed underneath, and every stored wrapping with it.
	after, err := damaged.shares.Get(ctx, key.KeyID)
	require.NoError(t, err)
	assert.True(t, before.Value.IsEqual(after.Value), "the rebuilt share differs from the original")
	assert.True(t, before.ID.IsEqual(after.ID), "the rebuilt share has a different index")

	// The node is a holder again, on the same key -- nothing rotated.
	active, err := damaged.ActiveKey(ctx)
	require.NoError(t, err)
	assert.Equal(t, key.KeyID, active.KeyID, "repair moved the epoch instead of rebuilding")

	// Serving a partial also needs the secret's wrapping to have replicated to
	// this node, which is independent of the repair.
	require.Eventually(t, func() bool {
		_, _, _, writer, err := damaged.Get(ctx, ref("owner-key"))
		if err != nil {
			return false
		}
		_, err = damaged.Partial(ctx, newest(ctx, damaged, ref("owner-key")), key.KeyID, writer)

		return err == nil
	}, settleFor, 100*time.Millisecond, "the repaired node still cannot serve partials")

	keys, err := c.services[0].keys.List(ctx)
	require.NoError(t, err)
	assert.Len(t, keys, 1, "repair should not have generated a new key")

	got, err := secrets.Get(ctx, ref("owner-key"))
	require.NoError(t, err)
	assert.Equal(t, plaintext, got)
}

func TestReplayOnlyServesTheCaller(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := newCluster(t, ctx, 3)
	c.bootstrap(t, ctx, 0)
	key := c.awaitKey(t, ctx)

	server := c.services[0]
	other := c.nodes[2].ID().String()

	// Asking for a third party's contribution is refused outright.
	_, err := server.replay(ctx, key.KeyID, "not-a-member-at-all")
	require.ErrorContains(t, err, "not a member")

	// And a member never replays its own contribution: it never went over the
	// wire in the first place.
	_, err = server.replay(ctx, key.KeyID, server.self())
	require.ErrorContains(t, err, "never goes over the wire")

	// A genuine member's contribution is served -- to that member.
	out, err := server.replay(ctx, key.KeyID, other)
	require.NoError(t, err)
	assert.Equal(t, server.self(), out.From)
	assert.NotEmpty(t, out.ShareValue)
}

func TestRetirementNeverDeletesANewerKey(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := newCluster(t, ctx, 2)
	c.bootstrap(t, ctx, 0)
	older := c.awaitKey(t, ctx)

	secrets := c.client(t)
	plaintext := []byte("wrapped under two keys")
	require.NoError(t, secrets.Set(ctx, ref("owner-key"), plaintext))

	srv := c.services[0]
	require.Eventually(t, func() bool {
		_, _, err := srv.wrappings.Get(ctx, newest(ctx, srv, ref("owner-key")), older.KeyID)

		return err == nil
	}, settleFor, 100*time.Millisecond, "the wrapping never reached this node")

	// A newer key, and a wrapping under it, that this node cannot open -- the
	// state of a node whose share of the newer key is broken.
	newer := &KeyMeta{
		KeyID:        "newer-key-this-node-cannot-open",
		Epoch:        older.Epoch + 1,
		Public:       older.Public,
		Verification: older.Verification,
		Threshold:    older.Threshold,
		Members:      older.Members,
	}
	require.NoError(t, srv.keys.Put(ctx, newer))

	public, err := newer.PublicKey()
	require.NoError(t, err)
	wrapping, err := tdh.Wrap(public, make([]byte, 32), wrapAAD(newest(ctx, srv, ref("owner-key")), newer.KeyID))
	require.NoError(t, err)
	require.NoError(t, srv.wrappings.Put(ctx, newest(ctx, srv, ref("owner-key")), newer.KeyID, "elsewhere", wrapping))

	// Retire while serving the OLDER key, which is what a node with a broken
	// newer share does.
	require.NoError(t, srv.retire(ctx, older))

	keyIDs, err := srv.wrappings.KeyIDs(ctx, newest(ctx, srv, ref("owner-key")))
	require.NoError(t, err)
	assert.Contains(t, keyIDs, newer.KeyID,
		"retiring against an older key deleted a newer key's wrapping")

	got, err := secrets.Get(ctx, ref("owner-key"))
	require.NoError(t, err)
	assert.Equal(t, plaintext, got)
}

func TestRotatesWhenTheKeyAgesOut(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const age = time.Hour
	clock := newFakeClock()

	c := newCluster(t, ctx, 2, WithRotationAge(age), WithClock(clock.now))
	c.bootstrap(t, ctx, 0)
	first := c.awaitKey(t, ctx)

	secrets := c.client(t)
	plaintext := []byte("must survive an age-triggered rotation")
	require.NoError(t, secrets.Set(ctx, ref("owner-key"), plaintext))

	// While the key is young, reconciliation must leave it alone however often
	// it runs -- otherwise a later epoch bump would prove nothing about age.
	for range 3 {
		c.reconcileAll(ctx)
	}

	active, err := c.services[0].ActiveKey(ctx)
	require.NoError(t, err)
	require.Equal(t, first.KeyID, active.KeyID, "a young key was rotated")

	// Let it age out.
	clock.advance(age + time.Minute)

	var second *KeyMeta
	require.Eventually(t, func() bool {
		c.reconcileAll(ctx)

		key, err := c.services[0].ActiveKey(ctx)
		if err != nil || key.Epoch == first.Epoch {
			return false
		}
		second = key

		return true
	}, 60*time.Second, 200*time.Millisecond, "the key never rotated after ageing out")

	assert.Greater(t, second.Epoch, first.Epoch)
	assert.NotEqual(t, first.KeyID, second.KeyID, "rotation must regenerate, not reshare")

	// Age, not membership: the roster is exactly what it was.
	assert.Equal(t, first.Members, second.Members, "the roster changed, so age was not the trigger")

	// And the secret came across.
	got, err := secrets.Get(ctx, ref("owner-key"))
	require.NoError(t, err)
	assert.Equal(t, plaintext, got)
}

func TestZeroRotationAgeDisablesTheTrigger(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	clock := newFakeClock()
	c := newCluster(t, ctx, 2, WithRotationAge(0), WithClock(clock.now))
	c.bootstrap(t, ctx, 0)
	first := c.awaitKey(t, ctx)

	clock.advance(365 * 24 * time.Hour)
	for range 5 {
		c.reconcileAll(ctx)
	}

	active, err := c.services[0].ActiveKey(ctx)
	require.NoError(t, err)
	assert.Equal(t, first.KeyID, active.KeyID, "a zero rotation age still rotated")
}

// fakeClock is a clock a test moves by hand.
type fakeClock struct {
	mu sync.Mutex
	at time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{at: time.Unix(1_700_000_000, 0)} }

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.at = c.at.Add(d)
	c.mu.Unlock()
}

func TestABootstrapWaitsForItsOwnRoundWhenAPeerFinishedItFirst(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := newCluster(t, ctx, 2)
	self, peer := c.services[0], c.services[1]

	members := self.roster()
	epoch, err := self.keys.Epoch(ctx)
	require.NoError(t, err)
	id := roundID(epoch, members)

	// This node is in the round and has not finished it.
	r, err := self.joinRound(ctx, id, epoch, members)
	require.NoError(t, err)

	// The peer has: it serves the round's key, holding a share of it.
	scalar := tdh.Group.RandomScalar(rand.Reader)
	share := secretsharing.Share{ID: tdh.Group.NewScalar().SetUint64(1), Value: scalar}
	require.NoError(t, peer.shares.Put(ctx, id, share))
	public, err := tdh.Group.NewElement().MulGen(scalar).MarshalBinaryCompress()
	require.NoError(t, err)
	require.NoError(t, peer.keys.Put(ctx, &KeyMeta{KeyID: id, Epoch: epoch, Public: public, Threshold: 1, Members: members}))

	done := make(chan error, 1)
	go func() { done <- self.ensureKey(ctx) }()

	select {
	case err := <-done:
		t.Fatalf("ensureKey returned before its own round completed: %v", err)
	case <-time.After(500 * time.Millisecond):
	}

	close(r.done)
	require.NoError(t, <-done)
}

func TestABootstrapStillRefusesAKeyItIsNotPartOf(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := newCluster(t, ctx, 2)
	self, peer := c.services[0], c.services[1]

	others := []string{peer.self()}
	id := roundID(0, others)
	scalar := tdh.Group.RandomScalar(rand.Reader)
	require.NoError(t, peer.shares.Put(ctx, id, secretsharing.Share{ID: tdh.Group.NewScalar().SetUint64(1), Value: scalar}))
	public, err := tdh.Group.NewElement().MulGen(scalar).MarshalBinaryCompress()
	require.NoError(t, err)
	require.NoError(t, peer.keys.Put(ctx, &KeyMeta{KeyID: id, Public: public, Threshold: 1, Members: others}))

	require.ErrorContains(t, self.ensureKey(ctx), "waiting to replicate it")
}
