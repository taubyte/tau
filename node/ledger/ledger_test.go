package ledger_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/node/ledger"
	"github.com/taubyte/tau/pkg/database"
	"github.com/taubyte/tau/pkg/raft"
	databaseService "github.com/taubyte/tau/services/database"
)

// ledger_test is `consensus: raft`: the store, and the table of groups it is
// opened over.
//
// WHAT IS UNDER TEST IS THE GATING AND NOT THE CONSENSUS. hashicorp/raft orders
// the writes; what this package has to hold is that a node which may not answer
// says so, names who may, and never answers an empty ledger for a group it
// cannot reach.
//
// EVERY ONE OF THESE USED TO NEED A NODE — a mocknet host, a register, a worker
// and a started composition — to assert what a ledger answers. What a ledger
// depends on is one function now, so a group that will not form, a group that
// was closed underneath a caller and a barrier with no quorum are all ordinary
// fixtures.

// ─────────────────────────────── doubles ───────────────────────────────

// follower is a cluster that is not the leader, or that fails where raft fails.
// It exists because the mock is a no-op leader — every write succeeds — and
// leadership is exactly what the writes here argue about: pkg/raft has no
// conditional write, so "am I the leader" plus a barrier is the whole of why a
// ledger's read is safe.
type follower struct {
	*raft.MockCluster

	// leader, when false, is a node that lost the election between being asked
	// and answering.
	leader bool

	// barrier is what raft answered a read's leadership proof with.
	barrier error

	// leaderID is who leads instead of this node. A ledger's refusal carries it,
	// which is what makes the call re-issuable rather than merely refused, so a
	// fixture that could not name one could not stand in for a follower at all.
	leaderID    libp2ppeer.ID
	leaderError error

	// stopsLeadingAfterBarrier is a group that goes away between the barrier and
	// the read — the window missed() exists for. Leadership is true when it is
	// proved and false by the time the state machine answers, and from then on
	// every answer is empty: which is what a closed cluster and a nil state
	// machine both look like.
	//
	// IT IS ARMED BY THE BARRIER AND NOT BY THE TEST, because a flag that also
	// flipped IsLeader before the proof would be refused by leading() and the
	// check after the read would never run. That is how the check after the read
	// went unproven for List, whose Keys cannot fail at all.
	stopsLeadingAfterBarrier bool
	lost                     bool

	// wrote is what a Set or a Delete answered, and is where a lost lease shows
	// up for a ledger.
	wrote error

	// closeErr is a group that would not shut down cleanly, which Close has to
	// carry rather than swallow.
	closeErr error

	// barriers, budgets and closed are what a caller DID, for the three claims
	// that are about the call rather than about its answer: that a read takes a
	// barrier, what one operation was allowed to cost, and that a node closes
	// the groups it opened.
	mu       sync.Mutex
	barriers int
	budgets  []time.Duration
	closed   bool
}

func notLeading() *follower {
	return &follower{MockCluster: raft.NewMockCluster()}
}

func leading() *follower {
	f := notLeading()
	f.leader = true
	return f
}

func (f *follower) IsLeader() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.leader && !f.lost
}

func (f *follower) Namespace() string { return "/tau/databases/v2/shop/prod/kv/QmLedger" }

func (f *follower) Get(key string) ([]byte, bool) {
	if f.gone() {
		return nil, false
	}
	return f.MockCluster.Get(key)
}

func (f *follower) Keys(prefix string) []string {
	if f.gone() {
		return nil
	}
	return f.MockCluster.Keys(prefix)
}

func (f *follower) gone() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lost
}

// relead puts the leadership back, keeping the group armed to lose it again on
// the next barrier — which is how the window is reached twice in one test.
func (f *follower) relead() {
	f.mu.Lock()
	f.lost = false
	f.mu.Unlock()
}

func (f *follower) Leader() (libp2ppeer.ID, error) {
	return f.leaderID, f.leaderError
}

func (f *follower) Barrier(d time.Duration) error {
	f.mu.Lock()
	f.barriers++
	f.budgets = append(f.budgets, d)
	if f.stopsLeadingAfterBarrier {
		f.lost = true
	}
	f.mu.Unlock()

	if f.barrier != nil {
		return f.barrier
	}
	return f.MockCluster.Barrier(d)
}

func (f *follower) Set(key string, value []byte, d time.Duration) error {
	f.spent(d)
	if f.wrote != nil {
		return f.wrote
	}
	return f.MockCluster.Set(key, value, d)
}

func (f *follower) Delete(key string, d time.Duration) error {
	f.spent(d)
	if f.wrote != nil {
		return f.wrote
	}
	return f.MockCluster.Delete(key, d)
}

func (f *follower) Close() error {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	if f.closeErr != nil {
		return f.closeErr
	}
	return f.MockCluster.Close()
}

func (f *follower) spent(d time.Duration) {
	f.mu.Lock()
	f.budgets = append(f.budgets, d)
	f.mu.Unlock()
}

// did is what a caller did to this cluster: how many barriers it took, what each
// operation was allowed to cost, and whether it closed the group.
func (f *follower) did() (barriers int, budgets []time.Duration, closed bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.barriers, append([]time.Duration(nil), f.budgets...), f.closed
}

// ─────────────────────────────── fixtures ───────────────────────────────

func ledgerSpec() database.Spec {
	return database.Spec{
		Type: database.KV, Engine: database.Tau, Consensus: database.Raft,
		Project: "shop", Environment: "prod", Name: "QmLedger",
	}
}

// opens is a Groups that answers one cluster and counts how many times it was
// asked, which is what "one group per database" is measured with.
type opens struct {
	mu     sync.Mutex
	count  int
	answer func() (raft.Cluster, error)
	hold   chan struct{}
}

func answering(c raft.Cluster) *opens {
	return &opens{answer: func() (raft.Cluster, error) { return c, nil }}
}

func (o *opens) Groups(string) (raft.Cluster, error) {
	if o.hold != nil {
		<-o.hold
	}
	o.mu.Lock()
	o.count++
	o.mu.Unlock()
	return o.answer()
}

func (o *opens) asked() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.count
}

// formed is a table whose one group is open, and the ledger over it.
//
// THE WAIT IS WHAT A CALLER WOULD DO, because opening is a background job by
// design — see Ledgers.group. It is polled through a Delete rather than a read:
// Delete takes no barrier, so waiting for the group does not spend the count the
// barrier tests are about.
func formed(t *testing.T, open ledger.Groups, budget time.Duration) (*ledger.Ledgers, database.Store) {
	t.Helper()

	l, err := ledger.New(open, budget)
	require.NoError(t, err)
	t.Cleanup(func() { l.Close() })

	store, err := l.Store(ledgerSpec())
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return !errors.Is(store.Delete(t.Context(), "probe"), databaseService.ErrGroupForming)
	}, 5*time.Second, time.Millisecond, "the group never finished forming")
	return l, store
}

// over is the ledger over one cluster, formed.
func over(t *testing.T, c raft.Cluster) database.Store {
	t.Helper()
	_, store := formed(t, answering(c).Groups, time.Second)
	return store
}

// ─────────────────────────────── the name ───────────────────────────────

// TestTHEGROUPISNAMEDBYWHATADDRESSESTHEDATABASE, under this package's own root:
// the same Spec.Prefix node/database gives its crdt store, so the group and the
// store agree by construction rather than by two expressions that happen to
// match.
func TestTheGroupIsNamedByWhatAddressesTheDatabase(t *testing.T) {
	name, err := ledger.Namespace(ledgerSpec())
	require.NoError(t, err)
	require.Equal(t, ledger.Domain+"/shop/prod/kv/QmLedger", name)

	// AND A SPEC THAT ADDRESSES NOTHING NAMES NOTHING. A separator in a segment
	// is a readable path into somebody else's ledger.
	_, err = ledger.Namespace(database.Spec{Type: database.KV, Name: "ledger"})
	require.ErrorIs(t, err, database.ErrUnaddressable)

	// AND THE TABLE REFUSES IT FOR THE SAME REASON, rather than opening a group
	// on a name it could not build.
	l, err := ledger.New(answering(leading()).Groups, time.Second)
	require.NoError(t, err)
	t.Cleanup(func() { l.Close() })

	_, err = l.Store(database.Spec{Type: database.KV, Name: "ledger"})
	require.ErrorIs(t, err, database.ErrUnaddressable)
}

// TestANODEWITHNOWHERETOOPENAGROUPKEEPSNOLEDGER, which is what makes the
// service's refusal by name true: the composer either handed this node a way to
// open a group or it did not.
func TestANodeWithNowhereToOpenAGroupKeepsNoLedger(t *testing.T) {
	_, err := ledger.New(nil, 0)
	require.Error(t, err)
	require.ErrorContains(t, err, "no way to open a group")
}

// ─────────────────────────── leadership ───────────────────────────

// TestAWRITEOFFTHELEADERISREFUSEDANDNAMESWHOLEADS.
//
// `raft: one holder at a time, so a write costs a round trip and nothing is
// silently discarded.` A follower that accepted the write would have to merge it
// later, which is the crdt promise wearing the other one's name.
func TestALedgerRefusesAWriteOffItsLeaderAndSaysWhoLeads(t *testing.T) {
	cluster := notLeading()
	cluster.leaderID = followerLeader(t)
	store := over(t, cluster)

	var elsewhere databaseService.NotLeading
	err := store.Put(t.Context(), "entry-1", []byte("one"))
	require.ErrorIs(t, err, databaseService.ErrNotLeading)
	require.True(t, errors.As(err, &elsewhere))
	require.Equal(t, cluster.leaderID.String(), elsewhere.Leader,
		"the refusal did not carry the one thing that makes the call re-issuable")

	require.ErrorIs(t, store.Delete(t.Context(), "entry-1"), databaseService.ErrNotLeading)

	_, _, err = store.Get(t.Context(), "entry-1")
	require.ErrorIs(t, err, databaseService.ErrNotLeading)

	keys, err := store.List(t.Context(), "")
	require.ErrorIs(t, err, databaseService.ErrNotLeading)
	require.Nil(t, keys)

	// A GROUP WITH NO LEADER AT ALL IS THE SAME ARM WITH NOBODY IN IT, so a
	// caller is told to ask again rather than to ask a node that does not exist.
	cluster.leaderError = errors.New("no leader")
	err = store.Put(t.Context(), "entry-1", []byte("one"))
	require.ErrorIs(t, err, databaseService.ErrNotLeading)
	require.True(t, errors.As(err, &elsewhere))
	require.Empty(t, elsewhere.Leader)
}

// followerLeader is a peer id a test can name, parsed once.
func followerLeader(t *testing.T) libp2ppeer.ID {
	t.Helper()
	id, err := libp2ppeer.Decode("12D3KooWDpJ7As7BWAwRMfu1VU2WCqNjvq387JEYKDBj4kx6nXTN")
	require.NoError(t, err)
	return id
}

// TestLOSINGTHELEASEMIDWRITEISSTILLNOTLEADING. Leadership can go between the
// check and the apply; when it does, raft says so, and the caller is told the
// same thing it would have been told a moment earlier rather than being handed a
// store failure it gives up on.
func TestLosingLeadershipMidWriteIsStillNotLeading(t *testing.T) {
	cluster := leading()
	cluster.wrote = raft.ErrNotLeader
	store := over(t, cluster)

	require.ErrorIs(t, store.Put(t.Context(), "entry-1", []byte("one")), databaseService.ErrNotLeading)
	require.ErrorIs(t, store.Delete(t.Context(), "entry-1"), databaseService.ErrNotLeading)

	// And a write that failed for any other reason is a write that failed.
	cluster.wrote = errors.New("the log is full")
	require.ErrorContains(t, store.Put(t.Context(), "entry-1", []byte("one")), "the log is full")
	require.ErrorContains(t, store.Delete(t.Context(), "entry-1"), "the log is full")
}

// TestAREADTAKESABARRIER, because applied state without one can be older than
// the write that already returned to its caller — and because a partitioned
// leader answers IsLeader true until its lease runs out, so the barrier is the
// leadership proof as much as it is the lag fix.
func TestALedgerReadsBehindABarrier(t *testing.T) {
	cluster := leading()
	store := over(t, cluster)

	require.NoError(t, store.Put(t.Context(), "entry-1", []byte("one")))

	before, _, _ := cluster.did()

	value, held, err := store.Get(t.Context(), "entry-1")
	require.NoError(t, err)
	require.True(t, held)
	require.Equal(t, []byte("one"), value)

	keys, err := store.List(t.Context(), "entry")
	require.NoError(t, err)
	require.Equal(t, []string{"entry-1"}, keys)

	after, _, _ := cluster.did()
	require.Equal(t, 2, after-before, "a read reached applied state without proving it was current")

	// A KEY NOBODY WROTE IS A MISS AND NOT AN ERROR: the store's own answer,
	// which the engine above turns into the one arm the vocabulary names.
	_, held, err = store.Get(t.Context(), "never-written")
	require.NoError(t, err)
	require.False(t, held)

	// AND A PREFIX NOTHING IS UNDER IS AN EMPTY ANSWER, on a node that still
	// leads. "Nothing is written here" is a fact a caller acts on, so the check
	// after the read has to be able to say nothing at all.
	empty, err := store.List(t.Context(), "nothing-under-here")
	require.NoError(t, err)
	require.Empty(t, empty)
}

// TestABARRIERTHATFAILSISNOTANEMPTYLEDGER.
//
// Keys CANNOT fail: a closed cluster and a nil state machine both answer an empty
// slice, so a group that is gone and a ledger nobody has written to are one
// answer. "The ledger is empty" is a fact a caller acts on, so the check that can
// fail has to be the one that runs first.
func TestABarrierThatFailsIsNotAnEmptyLedger(t *testing.T) {
	cluster := leading()
	cluster.barrier = errors.New("no quorum")
	require.NoError(t, cluster.MockCluster.Set("entry-1", []byte("one"), time.Second))
	store := over(t, cluster)

	keys, err := store.List(t.Context(), "")
	require.Error(t, err, "a ledger behind a failed barrier answered as if it were empty")
	require.Nil(t, keys)
	require.ErrorContains(t, err, "behind a barrier")
	require.ErrorContains(t, err, cluster.Namespace(),
		"the refusal did not say which ledger could not be read")

	_, _, err = store.Get(t.Context(), "entry-1")
	require.ErrorContains(t, err, "no quorum")
}

// TestAMISSFROMAGROUPTHATISGONEISNOTAMISS.
//
// A closed cluster and a nil state machine both answer empty, and the kv engine
// turns that into "no such key" — which is a fact a ledger's reader acts on. The
// barrier proves leadership BEFORE the read; this is the window after it.
func TestAMissFromAGroupThatIsGoneIsNotAMiss(t *testing.T) {
	cluster := leading()
	store := over(t, cluster)

	// A leader that has the key answers it, and one that has not answers a
	// miss: both of those are the ledger talking.
	require.NoError(t, store.Put(t.Context(), "entry-1", []byte("one")))
	_, held, err := store.Get(t.Context(), "never-written")
	require.NoError(t, err)
	require.False(t, held)

	// A NODE THAT NO LONGER LEADS DID NOT ANSWER A MISS — IT DID NOT ANSWER.
	// leading() and the barrier both ran while it still led.
	cluster.stopsLeadingAfterBarrier = true

	_, _, err = store.Get(t.Context(), "entry-1")
	require.ErrorIs(t, err, databaseService.ErrNotLeading,
		"a group that went away between the barrier and the read answered an empty ledger")

	// AND THE SAME WINDOW FOR List, which is where it matters most: Keys cannot
	// fail, so an empty slice is a ledger nobody wrote to AND a group that is
	// gone, and only the check after the read tells them apart.
	cluster.relead()
	keys, err := store.List(t.Context(), "")
	require.ErrorIs(t, err, databaseService.ErrNotLeading)
	require.Nil(t, keys)
}

// ─────────────────────────────── the budget ───────────────────────────────

// TestACANCELLEDCALLERDOESNOTPAYTHEFULLBUDGET. pkg/raft takes a duration and not
// a context, so a caller that has already given up would otherwise hold a commit
// open for the whole of the ledger's budget — on a request path where the gateway
// timed out seconds ago.
func TestACancelledCallerDoesNotPayTheFullBudget(t *testing.T) {
	cluster := leading()
	store := over(t, cluster)

	gone, stop := context.WithCancel(t.Context())
	stop()
	require.ErrorIs(t, store.Put(gone, "entry-1", []byte("one")), context.Canceled)
	require.ErrorIs(t, store.Delete(gone, "entry-1"), context.Canceled)
	_, _, err := store.Get(gone, "entry-1")
	require.ErrorIs(t, err, context.Canceled)
	_, err = store.List(gone, "")
	require.ErrorIs(t, err, context.Canceled)

	// AND A DEADLINE SHORTER THAN THE LEDGER'S IS THE ONE THAT REACHES RAFT,
	// which is the claim: the budget is not a number this package keeps to
	// itself.
	soon, done := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer done()
	require.NoError(t, store.Put(soon, "entry-2", []byte("two")))

	_, budgets, _ := cluster.did()
	require.NotEmpty(t, budgets)
	require.LessOrEqual(t, budgets[len(budgets)-1], 20*time.Millisecond,
		"the caller's deadline was not what bounded the call")

	// AND A DEADLINE ALREADY PAST IS NO BUDGET AT ALL. pkg/raft reads zero as
	// "do not wait", so a call that reached here with nothing left would be
	// issued against the group anyway and refused for the wrong reason.
	require.ErrorIs(t, store.Put(expired{t.Context()}, "entry-3", []byte("three")),
		context.DeadlineExceeded)
}

// expired is a caller whose deadline has passed and whose cancellation has not
// arrived: the window between the two that context.WithDeadline closes
// synchronously and a caller holding a deadline from somewhere else does not.
type expired struct{ context.Context }

func (expired) Deadline() (time.Time, bool) { return time.Now().Add(-time.Second), true }
func (expired) Err() error                  { return nil }

// TestTHEDEFAULTBUDGETISTHEPACKAGES, so a composer that states nothing gets the
// one written down rather than zero — which pkg/raft would read as "no wait at
// all".
func TestACallerWithNoDeadlinePaysTheLedgersBudget(t *testing.T) {
	cluster := leading()
	_, store := formed(t, answering(cluster).Groups, 0)

	require.NoError(t, store.Put(t.Context(), "entry-1", []byte("one")))

	_, budgets, _ := cluster.did()
	require.NotEmpty(t, budgets)
	require.Equal(t, ledger.Budget, budgets[len(budgets)-1])
}

// ─────────────────────────── the table of groups ───────────────────────────

// TestAGROUPTHATHASNOTFORMEDISREFUSEDANDNEVERWAITEDON.
//
// raft.New blocks for as long as thirty seconds looking for peers so that a
// cloud does not bootstrap a single-node cluster it later has to heal. That is
// right at boot and wrong on a request path: node/queue.go measured what it
// costs, which is every caller queued behind the first one and an invocation
// that timed out.
func TestAGroupThatHasNotFormedIsRefusedAndNeverWaitedOn(t *testing.T) {
	cluster := leading()
	asked := answering(cluster)
	asked.hold = make(chan struct{})

	l, err := ledger.New(asked.Groups, time.Second)
	require.NoError(t, err)
	t.Cleanup(func() { l.Close() })

	store, err := l.Store(ledgerSpec())
	require.NoError(t, err)

	// THE FIRST ASK ANSWERS IMMEDIATELY, which is the whole claim.
	done := make(chan error, 1)
	go func() { done <- store.Put(t.Context(), "entry-1", []byte("one")) }()
	select {
	case err := <-done:
		require.ErrorIs(t, err, databaseService.ErrGroupForming)
	case <-time.After(5 * time.Second):
		t.Fatal("a caller waited on a group that was still forming")
	}

	close(asked.hold)
	require.Eventually(t, func() bool {
		return store.Put(t.Context(), "entry-1", []byte("one")) == nil
	}, 10*time.Second, 10*time.Millisecond)

	// ONE GROUP PER DATABASE AND ONE OPEN PER GROUP: a second ask joins the
	// first rather than starting an election beside it.
	for range 5 {
		second, err := l.Store(ledgerSpec())
		require.NoError(t, err)
		require.NoError(t, second.Put(t.Context(), "entry-2", []byte("two")))
	}
	require.Equal(t, 1, asked.asked())

	// AND CLOSING THE TABLE CLOSES THE GROUPS IT OPENED. A cluster left running
	// keeps a protocol handler, a heartbeat and a log open on a peer nobody is
	// using.
	require.NoError(t, l.Close())
	_, _, shut := cluster.did()
	require.True(t, shut)
}

// TestAGROUPTHATCOULDNOTBEOPENEDSAYSSOANDISTRIEDAGAIN.
//
// It says so rather than looking like one that has not finished forming — an
// operator retrying forever is the failure a named refusal exists to stop. And it
// is tried again, because raft.New spends thirty seconds on whatever the disk,
// the host and the network were doing at the time: a failure left in the table
// would make one bad minute permanent for that ledger on this node.
func TestAGroupThatCouldNotBeOpenedSaysSoAndIsTriedAgain(t *testing.T) {
	asked := &opens{answer: func() (raft.Cluster, error) { return nil, errors.New("no disk") }}

	l, err := ledger.New(asked.Groups, time.Second)
	require.NoError(t, err)
	t.Cleanup(func() { l.Close() })

	store, err := l.Store(ledgerSpec())
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		err := store.Put(t.Context(), "entry-1", []byte("one"))
		return err != nil && !errors.Is(err, databaseService.ErrGroupForming) &&
			strings.Contains(err.Error(), "no disk")
	}, 10*time.Second, 10*time.Millisecond, "the reason the group could not be opened never reached a caller")

	// AND EVERY ASK AFTER A FAILURE IS A FRESH ATTEMPT rather than the same
	// answer read back out of the table.
	require.Eventually(t, func() bool {
		store.Put(t.Context(), "entry-1", []byte("one"))
		return asked.asked() > 1
	}, 10*time.Second, 10*time.Millisecond, "a group that failed to open was never tried again")
}

// TestAGROUPTHATOPENEDTONOTHINGISNOTAGROUPTHATISFORMING. An opener answering no
// cluster and no reason is a node on its way out; a caller told "still forming"
// retries here for a group that will never form, and what it should do is ask
// another voter.
func TestAGroupThatOpenedToNothingIsRefusedAsClosed(t *testing.T) {
	asked := &opens{answer: func() (raft.Cluster, error) { return nil, nil }}

	l, err := ledger.New(asked.Groups, time.Second)
	require.NoError(t, err)
	t.Cleanup(func() { l.Close() })

	store, err := l.Store(ledgerSpec())
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return errors.Is(store.Put(t.Context(), "k", nil), databaseService.ErrGroupClosed)
	}, 10*time.Second, 10*time.Millisecond)

	// AND CLOSING A TABLE WHOSE GROUP IS NOTHING IS NOT AN ERROR: there is
	// nothing to shut down and nothing to report.
	require.NoError(t, l.Close())
}

// TestANODEONITSWAYOUTOPENSNOMOREGROUPS.
//
// Close empties the table, and a request already past the resident lookup would
// otherwise open a fresh one afterwards — a live voter with a protocol handler, a
// heartbeat and a vote in a quorum, that nothing will ever close again. A node
// that has shut down would leave it behind.
func TestANodeOnItsWayOutOpensNoMoreGroups(t *testing.T) {
	cluster := leading()
	asked := answering(cluster)

	l, store := formed(t, asked.Groups, time.Second)
	require.NoError(t, l.Close())

	// AND THE REFUSAL IS NOT "STILL FORMING", which is the distinction the
	// caller acts on: told that, it retries here for a group that will never
	// form; told this, it asks another voter.
	other, err := l.Store(database.Spec{
		Type: database.KV, Engine: database.Tau, Consensus: database.Raft,
		Project: "shop", Environment: "prod", Name: "QmOther",
	})
	require.NoError(t, err)

	err = other.Put(t.Context(), "entry-1", []byte("one"))
	require.ErrorIs(t, err, databaseService.ErrGroupClosed)
	require.NotErrorIs(t, err, databaseService.ErrGroupForming)
	require.ErrorIs(t, err, databaseService.ErrNotHere,
		"a node on its way out did not send the caller anywhere else")

	// The one this node already had is refused the same way, and no second
	// election was started for either.
	require.ErrorIs(t, store.Put(t.Context(), "entry-1", []byte("one")), databaseService.ErrGroupClosed)
	require.Equal(t, 1, asked.asked(), "a node that had shut down opened another raft group")
}

// TestCLOSINGCARRIESWHATAGROUPSAIDONITSWAYDOWN, rather than reporting a clean
// shutdown for a group that did not have one.
func TestClosingCarriesWhatAGroupSaidOnItsWayDown(t *testing.T) {
	cluster := leading()
	cluster.closeErr = errors.New("the log would not flush")

	l, _ := formed(t, answering(cluster).Groups, time.Second)
	require.ErrorContains(t, l.Close(), "the log would not flush")
}

// TestCLOSINGWAITSFORAGROUPTHATISSTILLFORMING, because tearing one down
// mid-bootstrap would leave the goroutine writing into a closed store.
func TestClosingWaitsForAGroupThatIsStillForming(t *testing.T) {
	cluster := leading()
	asked := answering(cluster)
	asked.hold = make(chan struct{})

	l, err := ledger.New(asked.Groups, time.Second)
	require.NoError(t, err)

	_, err = l.Store(ledgerSpec())
	require.NoError(t, err)

	closed := make(chan error, 1)
	go func() { closed <- l.Close() }()

	select {
	case <-closed:
		t.Fatal("Close returned while the group it has to shut down was still opening")
	case <-time.After(100 * time.Millisecond):
	}

	close(asked.hold)
	require.NoError(t, <-closed)
	_, _, shut := cluster.did()
	require.True(t, shut, "the group that finished forming during the close was left running")
}
