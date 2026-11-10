package secrets

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"connectrpc.com/connect"
	logging "github.com/ipfs/go-log/v2"
	peerCore "github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreKvdb "github.com/taubyte/tau/core/kvdb"
	peerNode "github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/p2p/peer/peertest"
	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/kvdb"
	secretsv2 "github.com/taubyte/tau/pkg/proto/gen/tau/secrets/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/secrets/v2/secretsv2connect"
	"github.com/taubyte/tau/pkg/secrets/tdh"
)

// single is one node running the service, reached through its own Client.
func single(t *testing.T, opts ...option) (*server, *Client, peerNode.Node) {
	t.Helper()
	ctx := t.Context()

	node := peertest.Mock(t, ctx)
	t.Cleanup(func() { node.Close() })
	db, err := kvdb.Open(node, fmt.Sprintf("secrets-%d", clusters.Add(1)), kvdb.WithLogger(logging.Logger("secrets-test")), kvdb.WithRebroadcast(time.Second))
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	srv, err := build(node.Host(), db, append([]option{
		withRoot(t.TempDir()), withBootstrapDelay(never), withConfig(Config{}),
		withMembers(func() []peerCore.ID { return nil }), withServerOptions(),
		withReconcileInterval(time.Hour), withReplacedRetention(0),
	}, opts...)...)
	require.NoError(t, err)
	require.NoError(t, srv.serve(ctx))
	t.Cleanup(func() { srv.Close() })

	client, err := NewClient(node.Host(), located{local: srv})
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })
	return srv, client, node
}

func TestALocalClientNeedsNoStreamAndFormsAKeyOnFirstWrite(t *testing.T) {
	_, client, _ := single(t)
	ctx := t.Context()

	require.NoError(t, client.Set(ctx, Ref{Scope: Project("shop", "prod"), Name: "token"}, []byte("sk_live")))
	got, err := client.Secret(ctx, "shop", "prod", "token")
	require.NoError(t, err)
	assert.Equal(t, "sk_live", got)

	names, err := client.List(ctx, Project("shop", "prod"))
	require.NoError(t, err)
	assert.Equal(t, []string{"token"}, names)

	_, err = client.Get(ctx, Ref{Scope: Project("shop", "prod"), Name: "absent"})
	require.ErrorIs(t, err, ErrNotSet)

	require.NoError(t, client.Delete(ctx, Ref{Scope: Project("shop", "prod"), Name: "token"}))
	_, err = client.Secret(ctx, "shop", "prod", "token")
	require.ErrorIs(t, err, ErrNotSet)

	_, err = client.List(ctx, Project("shop", ""))
	require.ErrorIs(t, err, ErrInvalidRef)
}

func TestAClientWithNobodyToAskSaysSo(t *testing.T) {
	node := peertest.Mock(t, t.Context())
	t.Cleanup(func() { node.Close() })
	client, err := NewClient(node.Host(), nil)
	require.NoError(t, err)

	require.ErrorIs(t, client.Set(t.Context(), Ref{Scope: Project("p", "e"), Name: "n"}, []byte("v")), ErrNoMembers)
	_, err = client.Get(t.Context(), Ref{Scope: Project("p", "e"), Name: "n"})
	require.ErrorIs(t, err, ErrNoMembers)
	_, err = client.List(t.Context(), Project("p", "e"))
	require.ErrorIs(t, err, ErrNoMembers)

	// A member named but unreachable is unavailable, not absent.
	unreachable := peertest.Mock(t, t.Context())
	t.Cleanup(func() { unreachable.Close() })
	client, err = NewClient(node.Host(), Peers{unreachable.ID(), node.ID()})
	require.NoError(t, err)
	err = client.Delete(t.Context(), Ref{Scope: Project("p", "e"), Name: "n"})
	require.ErrorIs(t, err, ErrUnavailable)

	_, err = NewClient(nil, nil)
	require.ErrorIs(t, err, ErrNoHost)
}

func TestWhatNewAndItsOptionsRefuse(t *testing.T) {
	node := peertest.Mock(t, t.Context())
	t.Cleanup(func() { node.Close() })
	db, err := kvdb.Open(node, fmt.Sprintf("secrets-%d", clusters.Add(1)), kvdb.WithLogger(logging.Logger("secrets-test")), kvdb.WithRebroadcast(time.Second))
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	_, err = build(nil, db)
	require.ErrorIs(t, err, ErrNoHost)
	_, err = build(node.Host(), nil)
	require.ErrorIs(t, err, ErrNoDB)
	_, err = build(node.Host(), db)
	require.ErrorIs(t, err, ErrNoRoot)

	for name, opt := range map[string]option{
		"root":      withRoot(""),
		"members":   withMembers(nil),
		"pubsub":    withPubSub(nil),
		"delay":     withBootstrapDelay(-1),
		"age":       withRotationAge(-1),
		"reconcile": withReconcileInterval(0),
		"clock":     withClock(nil),
	} {
		_, err := build(node.Host(), db, withRoot(t.TempDir()), opt)
		require.Error(t, err, name)
	}
}

func TestStartIsOnceAndNotAfterClose(t *testing.T) {
	srv, _, _ := single(t)
	require.Error(t, srv.serve(t.Context()))
	require.NoError(t, srv.Close())
	require.NoError(t, srv.Close(), "close is idempotent")
	require.Error(t, srv.serve(t.Context()))
}

func TestTheHandlersRefuseWhatTheyCannotUse(t *testing.T) {
	srv, _, _ := single(t)
	ctx := t.Context()
	h := &secretsHandler{srv}

	good := wireRef(Ref{Scope: Project("p", "e"), Name: "n"})
	_, err := h.Put(ctx, connect.NewRequest(&secretsv2.PutRequest{Ref: good}))
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	_, err = h.Put(ctx, connect.NewRequest(&secretsv2.PutRequest{Ref: &secretsv2.Ref{}}))
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	_, err = h.Put(ctx, connect.NewRequest(&secretsv2.PutRequest{Ref: good, KeyId: "nope", Ciphertext: []byte("x"), Wrapping: &secretsv2.Wrapping{}}))
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(err))
	_, err = h.Delete(ctx, connect.NewRequest(&secretsv2.Ref{}))
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	_, err = h.Get(ctx, connect.NewRequest(&secretsv2.Ref{}))
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	_, err = h.Partial(ctx, connect.NewRequest(&secretsv2.PartialRequest{}))
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	_, err = h.Partial(ctx, connect.NewRequest(&secretsv2.PartialRequest{Ref: good, KeyId: "nope"}))
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(err))
	_, err = h.Key(ctx, connect.NewRequest(&secretsv2.KeyRequest{KeyId: "nope"}))
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(err))
	_, err = h.Key(ctx, connect.NewRequest(&secretsv2.KeyRequest{}))
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(err), "a question about a key does not form one")

	d := &dealerHandler{srv}
	_, err = d.Replay(ctx, connect.NewRequest(&secretsv2.ReplayRequest{KeyId: "x"}))
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err), "replay names its caller, and a local call has none")
	_, err = d.Contribute(ctx, connect.NewRequest(&secretsv2.Contribution{Round: "x"}))
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))

	// A closing service writes nothing.
	require.NoError(t, srv.Close())
	_, err = h.Put(ctx, connect.NewRequest(&secretsv2.PutRequest{Ref: good}))
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(err))
	_, err = h.Delete(ctx, connect.NewRequest(good))
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(err))
	_, err = d.Contribute(ctx, connect.NewRequest(&secretsv2.Contribution{}))
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(err))
	_, err = h.Key(ctx, connect.NewRequest(&secretsv2.KeyRequest{Form: true}))
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(err))
}

func TestReplayOverTheWireNamesTheCaller(t *testing.T) {
	_, _, server := single(t)
	caller := peertest.Mock(t, t.Context())
	t.Cleanup(func() { caller.Close() })
	require.NoError(t, peertest.LinkAll(t))
	require.NoError(t, caller.Host().Connect(t.Context(), server.Host().Peerstore().PeerInfo(server.ID())))

	c, err := rpc.NewClient(caller.Host(), server.ID(), Protocol)
	require.NoError(t, err)
	defer c.Close()
	dealer := newDealerClient(c)
	_, err = dealer.Replay(t.Context(), connect.NewRequest(&secretsv2.ReplayRequest{KeyId: "unknown"}))
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
}

func TestWireCarriesTheSentinels(t *testing.T) {
	require.ErrorIs(t, unwire(wire(ErrInvalidRef)), ErrInvalidRef)
	require.ErrorIs(t, unwire(wire(ErrNotSet)), ErrNotSet)
	other := errors.New("other")
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(unwire(wire(other))))
}

func TestAGroupKeyThatCannotBeUsedIsRefused(t *testing.T) {
	_, err := groupKey(&secretsv2.GroupKey{Public: []byte("not a point")})
	require.Error(t, err)

	srv, client, _ := single(t)
	require.NoError(t, client.Set(t.Context(), Ref{Scope: Project("p", "e"), Name: "n"}, []byte("v")))
	key, err := srv.ActiveKey(t.Context())
	require.NoError(t, err)

	_, err = groupKey(&secretsv2.GroupKey{Public: key.Public, Members: []string{"not a peer"}, Threshold: 1})
	require.Error(t, err)
	_, err = groupKey(&secretsv2.GroupKey{Public: key.Public, Members: key.Members, Threshold: 2})
	require.Error(t, err)
}

func TestTheRoundTableStaysBounded(t *testing.T) {
	srv, _, _ := single(t)
	members := []string{srv.self(), "a-peer-that-never-answers"}
	for epoch := range uint64(maxRounds + 3) {
		_, err := srv.joinRound(t.Context(), roundID(epoch, members), epoch, members)
		require.NoError(t, err)
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	require.LessOrEqual(t, len(srv.rounds), maxRounds)
}

func TestAwaitGivesUpWithItsCaller(t *testing.T) {
	srv, _, _ := single(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, srv.await(ctx, &round{done: make(chan struct{})}), context.Canceled)
}

func newDealerClient(c *rpc.Client) secretsv2connect.DealerServiceClient {
	return secretsv2connect.NewDealerServiceClient(c.HTTP(), c.URL())
}

// broken is a database whose writes, lists and deletes fail, the way a store
// that is closing does.
type broken struct {
	coreKvdb.Instance
}

var errBroken = errors.New("store is broken")

func (broken) Put(context.Context, string, []byte) error      { return errBroken }
func (broken) Delete(context.Context, string) error           { return errBroken }
func (broken) List(context.Context, string) ([]string, error) { return nil, errBroken }
func (broken) Get(context.Context, string) ([]byte, error)    { return nil, errBroken }
func (broken) Compact(context.Context) (int, error)           { return 0, errBroken }

func TestRetiringCompactsTheRealStore(t *testing.T) {
	srv, _, _ := single(t)
	_, ok := srv.db.(coreKvdb.Compactor)
	require.True(t, ok, "the production store must be a Compactor")

	ctx := t.Context()
	for i := range 4 {
		key := fmt.Sprintf("/churn/%d", i)
		require.NoError(t, srv.db.Put(ctx, key, []byte("v")))
		require.NoError(t, srv.db.Delete(ctx, key))
	}
	counted := &countingCompactor{Instance: srv.db}
	srv.db = counted
	require.NoError(t, srv.compact(ctx))
	require.Equal(t, 1, counted.calls)
	require.Positive(t, counted.dropped, "compacting a store with history must drop some of it")
}

type countingCompactor struct {
	coreKvdb.Instance
	calls, dropped int
}

func (c *countingCompactor) Compact(ctx context.Context) (int, error) {
	c.calls++
	n, err := c.Instance.(coreKvdb.Compactor).Compact(ctx)
	c.dropped += n
	return n, err
}

func TestAStoreThatFailsIsAnErrorEverywhere(t *testing.T) {
	ctx := t.Context()
	ref := Ref{Scope: Project("p", "e"), Name: "n"}
	db := broken{}

	v := version{Ref: ref, CID: contentID([]byte("c"))}
	require.Error(t, (&recordStore{db: db}).Put(ctx, v, []byte("c"), 1))
	require.ErrorIs(t, (&recordStore{db: db}).Put(ctx, v, []byte("other"), 1), ErrInvalidRef)
	require.Error(t, (&recordStore{db: db}).Delete(ctx, v))
	_, err := (&recordStore{db: db}).Versions(ctx, ref)
	require.Error(t, err)
	_, err = (&recordStore{db: db}).All(ctx)
	require.Error(t, err)
	require.Error(t, (&wrappingStore{db: db}).Put(ctx, v, "k", "w", &tdh.Wrapping{}))
	require.Error(t, (&wrappingStore{db: db}).DeleteKey(ctx, v, "k"))
	require.Error(t, (&wrappingStore{db: db}).DeleteAll(ctx, v))
	_, err = (&keyStore{db: db}).Newest(ctx)
	require.Error(t, err)
	_, err = (&keyStore{db: db}).Epoch(ctx)
	require.Error(t, err)

	// Invalid references never reach the store.
	bad := version{}
	require.ErrorIs(t, (&recordStore{db: db}).Delete(ctx, bad), ErrInvalidRef)
	require.ErrorIs(t, (&wrappingStore{db: db}).Put(ctx, bad, "k", "w", &tdh.Wrapping{}), ErrInvalidRef)
	_, err = (&recordStore{db: db}).List(ctx, Project("", "e"))
	require.ErrorIs(t, err, ErrInvalidRef)
	_, err = (&recordStore{db: db}).Versions(ctx, Ref{})
	require.ErrorIs(t, err, ErrInvalidRef)

	srv := &server{db: db, records: &recordStore{db: db}, wrappings: &wrappingStore{db: db}, keys: &keyStore{db: db}}
	require.Error(t, srv.Delete(ctx, ref))
	require.Error(t, srv.compact(ctx))
	require.Error(t, srv.retire(ctx, &KeyMeta{}))
	require.Error(t, srv.prune(ctx))
	require.Error(t, srv.reEncrypt(ctx, &KeyMeta{}))
	require.Error(t, srv.dropGenesis(ctx))
}

func TestTheBootTimerFormsTheFirstKey(t *testing.T) {
	srv, _, _ := single(t, withBootstrapDelay(10*time.Millisecond))
	require.Eventually(t, func() bool {
		_, err := srv.ActiveKey(t.Context())
		return err == nil
	}, 10*time.Second, 20*time.Millisecond)
}

func TestTheAtRestKeyIsDerivedPerNode(t *testing.T) {
	node := peertest.Mock(t, t.Context())
	t.Cleanup(func() { node.Close() })
	a, err := newAtRest(node.Host(), "configured")
	require.NoError(t, err)
	_, err = a.open([]byte("short"))
	require.Error(t, err)
}

// located is the service found where a test says: these members, and this one
// beside the caller.
type located struct {
	members []peerCore.ID
	local   *server
}

func (l located) Members() []peerCore.ID { return l.members }
func (l located) beside() *server        { return l.local }
