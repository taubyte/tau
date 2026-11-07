package primitives

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	ds "github.com/ipfs/go-datastore"
	"github.com/ipfs/go-log/v2"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"
	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/pkg/invoke"
	kvdbmock "github.com/taubyte/tau/pkg/kvdb/mock"
	"github.com/taubyte/tau/pkg/raft"
	"github.com/taubyte/tau/pkg/register"
)

// A primitives service over a mock consensus and an in-memory record store: these
// tests are about what the service decides, which needs no real election.

const (
	projectID = "QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR"
	jobsID    = "QmVLDAzEjEbfBPuoyLPWnPMhKp8ceDXwGdmMKY5W3nZWfG"
	auditID   = "QmZ4gCbmYaFZ4dWJmvNBQKvNAWMYtHqQTHRDprHmxPh6Zk"
	tracesID  = "QmPq9zYJvZ8kLpQ2mWfFPd6Fy1kVYhZLb8vtNJcXAGL3Rd"
	gossipID  = "QmSgvGwSJmtqBCJ8Zw3rNKtCCTqTFcqSUQqLYyPBnW6cVs"
	deadID    = "QmXqE8kkR6cB9vkkeNZ7dMtsgPPmYzArkLfVtn9SBLsRJk"
	chatterID = "QmdWL1UYbDLtNcQFzJXgAmnfJv7CTNVnEbcbMkFf7GE4tZ"
	boundedID = "QmW5ewPvyQdwKAF6uEJTXzfUKQ99VWEAfBganZeD2r6ySc"
	sessionID = "QmbWqxBEKC3P8tqsKc98xmWNzrzDtRLMiMPL8wBuTGsMnR"
)

// compiled is a project with every shape these tests need: two logs that must
// not see each other, a queue, a queue that gives up after one delivery, a log
// asking for a consensus this service does not keep, and a database — so a
// refusal by name can be told from a refusal by type.
func compiled() []byte {
	doc := map[string]any{
		"object": map[string]any{
			"id": projectID, "name": "shop",
			"sync": map[string]any{
				auditID: map[string]any{
					"name": "audit", "type": "log",
					"log": map[string]any{"consensus": "raft"},
				},
				tracesID: map[string]any{
					"name": "traces", "type": "log",
					"log": map[string]any{
						"consensus": "raft",
						"retention": map[string]any{"age": float64(time.Hour)},
					},
				},
				gossipID: map[string]any{
					"name": "gossip", "type": "log",
					"log": map[string]any{"consensus": "crdt"},
				},
				jobsID: map[string]any{
					"name": "jobs", "type": "queue",
					"queue": map[string]any{
						"consensus":          "raft",
						"visibility_timeout": float64(time.Minute),
					},
				},
				chatterID: map[string]any{
					"name": "chatter", "type": "queue",
					"queue": map[string]any{"consensus": "crdt"},
				},
				boundedID: map[string]any{
					"name": "bounded", "type": "queue",
					"queue": map[string]any{
						"consensus": "raft",
						"retention": map[string]any{"age": float64(time.Hour), "size": float64(8)},
					},
				},
				deadID: map[string]any{
					"name": "fragile", "type": "queue",
					"queue": map[string]any{
						"consensus":          "raft",
						"visibility_timeout": float64(time.Second),
						"max_attempts":       float64(1),
					},
				},
			},
			"database": map[string]any{
				sessionID: map[string]any{
					"name": "sessions", "type": "kv",
					"kv": map[string]any{"engine": "distributed", "consensus": "crdt"},
				},
			},
		},

		// THE INDEX IS WHAT MAKES AN ID ADDRESSABLE. tcc emits
		// `projects/<pid>/<kind>/<name>/links` per resource, and pkg/specs reads
		// it backwards to answer "what is this id" — which is the only question
		// this service asks of a project.
		"indexes": map[string]any{
			"projects/" + projectID + "/sync/audit/links": []any{
				"branches/main/projects/" + projectID + "/sync/" + auditID},
			"projects/" + projectID + "/sync/traces/links": []any{
				"branches/main/projects/" + projectID + "/sync/" + tracesID},
			"projects/" + projectID + "/sync/bounded/links": []any{
				"branches/main/projects/" + projectID + "/sync/" + boundedID},
			"projects/" + projectID + "/sync/gossip/links": []any{
				"branches/main/projects/" + projectID + "/sync/" + gossipID},
			"projects/" + projectID + "/sync/jobs/links": []any{
				"branches/main/projects/" + projectID + "/sync/" + jobsID},
			"projects/" + projectID + "/sync/chatter/links": []any{
				"branches/main/projects/" + projectID + "/sync/" + chatterID},
			"projects/" + projectID + "/sync/fragile/links": []any{
				"branches/main/projects/" + projectID + "/sync/" + deadID},
			"projects/" + projectID + "/database/sessions/links": []any{
				"branches/main/projects/" + projectID + "/database/" + sessionID},
		},
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		panic(err)
	}
	return raw
}

// published is a register holding one deployment, so the service reads real
// declarations rather than being told what they are.
type published struct {
	revision uint64
	object   []byte
}

func (p *published) Resolve(context.Context, string, string) (register.Pointer, error) {
	return register.Pointer{Object: register.IDOf(p.object), Revision: p.revision}, nil
}

func (p *published) Object(_ context.Context, id register.ObjectID) ([]byte, error) {
	if id == register.IDOf(p.object) {
		return p.object, nil
	}
	return nil, fmt.Errorf("no such object")
}

// fakeCluster is raft.MockCluster plus a node that is not the leader; the mock alone
// always leads.
type fakeCluster struct {
	*raft.MockCluster

	mu       sync.Mutex
	follower bool
}

// oneGroup answers every namespace with the same group.
type oneGroup struct{ raft.Cluster }

func (g oneGroup) Group(string) (raft.Cluster, error) { return g.Cluster, nil }

// freshGroups opens a new fake group for every namespace.
type freshGroups struct{}

func (freshGroups) Group(string) (raft.Cluster, error) { return newCluster(), nil }

func newCluster() *fakeCluster {
	return &fakeCluster{MockCluster: raft.NewMockCluster()}
}

func (c *fakeCluster) stepDown() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.follower = true
}

func (c *fakeCluster) IsLeader() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.follower
}

func (c *fakeCluster) refuse() error {
	if c.IsLeader() {
		return nil
	}
	return raft.ErrNotLeader
}

func (c *fakeCluster) Set(key string, value []byte, timeout time.Duration) error {
	if err := c.refuse(); err != nil {
		return err
	}
	return c.MockCluster.Set(key, value, timeout)
}

func (c *fakeCluster) Delete(key string, timeout time.Duration) error {
	if err := c.refuse(); err != nil {
		return err
	}
	return c.MockCluster.Delete(key, timeout)
}

func (c *fakeCluster) Batch(ops []raft.BatchOp, timeout time.Duration) error {
	if err := c.refuse(); err != nil {
		return err
	}
	return c.MockCluster.Batch(ops, timeout)
}

func (c *fakeCluster) State() raft.State {
	if c.IsLeader() {
		return raft.Leader
	}
	return raft.Follower
}

// elsewhere is the node this fixture's group says may write: on no swarm, so a
// refusal naming it proves the destination travels and a tasking sent to it cannot
// arrive.
var elsewhere = libp2ppeer.ID("the-other-node")

// Leader names whoever may write, so a refusal can carry it.
func (c *fakeCluster) Leader() (libp2ppeer.ID, error) { return elsewhere, nil }

// brittle is a leader whose replicated write can fail (ErrNotLeader, or a timeout),
// which the mock cannot do.
type brittle struct {
	*fakeCluster

	mu       sync.Mutex
	refusing bool
}

func (b *brittle) refuseWrites(on bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.refusing = on
}

func (b *brittle) Batch(ops []raft.BatchOp, timeout time.Duration) error {
	b.mu.Lock()
	refusing := b.refusing
	b.mu.Unlock()
	if refusing {
		return raft.ErrNotLeader
	}
	return b.fakeCluster.Batch(ops, timeout)
}

// leader is what these tests need of a cluster beyond raft.Cluster: a node that
// stops being the one that may write.
type leader interface {
	raft.Cluster
	stepDown()
}

// stores is pkg/kvdb/mock answering a miss with ds.ErrNotFound, as core/kvdb does,
// and able to fail outright: log.go reads the two differently.
type stores struct {
	*kvdbmock.Factory

	mu   sync.Mutex
	sick bool
}

func newStores() *stores { return &stores{Factory: kvdbmock.New()} }

// sicken is a store that cannot answer — not one with nothing to say.
func (s *stores) sicken(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sick = on
}

func (s *stores) failing() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sick
}

func (s *stores) open(path string) (kvdb.Instance, error) {
	db, err := s.Factory.New(log.Logger("primitives"), path, 5)
	if err != nil {
		return nil, err
	}
	return &storeDB{Instance: db, stores: s}, nil
}

type storeDB struct {
	kvdb.Instance

	stores *stores
}

func (d *storeDB) Get(ctx context.Context, key string) ([]byte, error) {
	if d.stores.failing() {
		return nil, errors.New("the record store cannot answer")
	}
	raw, err := d.Instance.Get(ctx, key)
	if errors.Is(err, kvdbmock.ErrNotFound) {
		return nil, ds.ErrNotFound
	}
	return raw, err
}

// clock is a time this test moves by hand, because everything the lifecycle
// pass decides — what retention has outlived, what a claim has outlasted — is
// decided against it.
type clock struct {
	mu sync.Mutex
	at time.Time
}

func newClock() *clock {
	return &clock{at: time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)}
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = c.at.Add(d)
}

// set puts the clock at a stated moment, which is what a cron test needs of it:
// a schedule is written in wall-clock terms and the case is "03:00 came round",
// not "half an hour went by".
func (c *clock) set(at time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = at
}

// harness is one started service and the pieces a test moves underneath it.
type harness struct {
	*server
	cluster leader
	stores  *stores
	clock   *clock
}

// starting starts a service over a leader that always commits.
func starting(t *testing.T, opts ...option) *harness {
	t.Helper()
	return startingOn(t, newCluster(), opts...)
}

// startingOn starts a service and waits for its consensus to form, since the group
// forms off the request path. The cluster is the test's.
func startingOn(t *testing.T, cluster leader, opts ...option) *harness {
	t.Helper()

	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	host, err := mn.GenPeer()
	require.NoError(t, err)

	records := newStores()
	tick := newClock()

	s, err := build(host, append([]option{
		withRegister(&published{revision: 1, object: compiled()}),
		withConsensus(oneGroup{cluster}),
		withRecords(records.open),
		withClock(tick.now),

		// A period long enough never to fire; tests drive the pass directly.
		withSweep(time.Hour),
	}, opts...)...)
	require.NoError(t, err)

	require.NoError(t, s.serve(t.Context()))
	t.Cleanup(func() { s.Close() })

	s.formMu.Lock()
	forming := s.forming
	s.formMu.Unlock()
	<-forming

	return &harness{server: s, cluster: cluster, stores: records, clock: tick}
}

// grantFor is what a compute tier hands a seam: the handle, and nothing else.
func grantFor(id string) invoke.Grant {
	return invoke.Grant{Handle: projectID + "/prod/" + id, Label: id}
}

// stored is every key one log's records sit under. It is how a test sees what
// no reader can: a record whose position was never published is reachable
// through nothing else.
func (h *harness) stored(t *testing.T, id string) []string {
	t.Helper()

	_, db, err := h.records.ready()
	require.NoError(t, err)

	at := Spec{Project: projectID, Environment: "prod", Type: logType, Name: id}
	keys, err := db.List(t.Context(), at.Prefix()+"/records/")
	require.NoError(t, err)
	return keys
}

// mockRecords is pkg/kvdb/mock as the opener the service is given.
func mockRecords() Records {
	f := kvdbmock.New()
	return func(name string) (kvdb.Instance, error) { return f.New(log.Logger("primitives"), name, 5) }
}
