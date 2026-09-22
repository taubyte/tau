package node

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/database"
	"github.com/taubyte/tau/pkg/raft"
	databaseService "github.com/taubyte/tau/services/database"
)

// ledger_test is `consensus: raft` on this node: the store, and the group it is
// opened over.
//
// WHAT IS UNDER TEST IS THE GATING AND NOT THE CONSENSUS. hashicorp/raft orders
// the writes; what this file has to hold is that a node which may not answer
// says so, names who may, and never answers an empty ledger for a group it
// cannot reach.

// followerLeader is a peer id a test can name, parsed once.
func followerLeader(t *testing.T) libp2ppeer.ID {
	t.Helper()
	id, err := libp2ppeer.Decode("12D3KooWDpJ7As7BWAwRMfu1VU2WCqNjvq387JEYKDBj4kx6nXTN")
	require.NoError(t, err)
	return id
}

// over is a raftStore onto one cluster.
func over(c raft.Cluster) *raftStore {
	return &raftStore{
		group:   func() (raft.Cluster, error) { return c, nil },
		timeout: time.Second,
	}
}

// TestAWRITEOFFTHELEADERISREFUSEDANDNAMESWHOLEADS.
//
// `raft: one holder at a time, so a write costs a round trip and nothing is
// silently discarded.` A follower that accepted the write would have to merge
// it later, which is the crdt promise wearing the other one's name.
func TestALedgerRefusesAWriteOffItsLeaderAndSaysWhoLeads(t *testing.T) {
	cluster := notLeading()
	cluster.leaderID = followerLeader(t)
	store := over(cluster)

	var elsewhere databaseService.NotLeading
	err := store.Put(t.Context(), "entry-1", []byte("one"))
	require.ErrorIs(t, err, databaseService.ErrNotLeading)
	require.True(t, errors.As(err, &elsewhere))
	require.Equal(t, cluster.leaderID.String(), elsewhere.Leader,
		"the refusal did not carry the one thing that makes the call re-issuable")

	require.ErrorIs(t, store.Delete(t.Context(), "entry-1"), databaseService.ErrNotLeading)

	_, _, err = store.Get(t.Context(), "entry-1")
	require.ErrorIs(t, err, databaseService.ErrNotLeading)

	// A GROUP WITH NO LEADER AT ALL IS THE SAME ARM WITH NOBODY IN IT, so a
	// caller is told to ask again rather than to ask a node that does not
	// exist.
	cluster.leaderError = errors.New("no leader")
	err = store.Put(t.Context(), "entry-1", []byte("one"))
	require.ErrorIs(t, err, databaseService.ErrNotLeading)
	require.True(t, errors.As(err, &elsewhere))
	require.Empty(t, elsewhere.Leader)
}

// TestLOSINGTHELEASEMIDWRITEISSTILLNOTLEADING. Leadership can go between the
// check and the apply; when it does, raft says so, and the caller is told the
// same thing it would have been told a moment earlier rather than being handed
// a store failure it gives up on.
func TestLosingLeadershipMidWriteIsStillNotLeading(t *testing.T) {
	cluster := notLeading()
	cluster.leader = true
	cluster.wrote = raft.ErrNotLeader
	store := over(cluster)

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
	cluster := notLeading()
	cluster.leader = true
	store := over(cluster)

	require.NoError(t, store.Put(t.Context(), "entry-1", []byte("one")))

	value, held, err := store.Get(t.Context(), "entry-1")
	require.NoError(t, err)
	require.True(t, held)
	require.Equal(t, []byte("one"), value)

	keys, err := store.List(t.Context(), "entry")
	require.NoError(t, err)
	require.Equal(t, []string{"entry-1"}, keys)

	barriers, _ := cluster.did()
	require.Equal(t, 2, barriers, "a read reached applied state without proving it was current")

	// A KEY NOBODY WROTE IS A MISS AND NOT AN ERROR: the store's own answer,
	// which the engine above turns into the one arm the vocabulary names.
	_, held, err = store.Get(t.Context(), "never-written")
	require.NoError(t, err)
	require.False(t, held)
}

// TestABARRIERTHATFAILSISNOTANEMPTYLEDGER.
//
// Keys CANNOT fail: a closed cluster and a nil state machine both answer an
// empty slice, so a group that is gone and a ledger nobody has written to are
// one answer. "The ledger is empty" is a fact a caller acts on, so the check
// that can fail has to be the one that runs first.
func TestABarrierThatFailsIsNotAnEmptyLedger(t *testing.T) {
	cluster := notLeading()
	cluster.leader = true
	cluster.barrier = errors.New("no quorum")
	require.NoError(t, cluster.MockCluster.Set("entry-1", []byte("one"), time.Second))
	store := over(cluster)

	keys, err := store.List(t.Context(), "")
	require.Error(t, err, "a ledger behind a failed barrier answered as if it were empty")
	require.Nil(t, keys)
	require.ErrorContains(t, err, "behind a barrier")

	_, _, err = store.Get(t.Context(), "entry-1")
	require.ErrorContains(t, err, "no quorum")
}

// TestAGROUPTHATHASNOTFORMEDISREFUSEDANDNEVERWAITEDON.
//
// raft.New blocks for as long as thirty seconds looking for peers so that a
// cloud does not bootstrap a single-node cluster it later has to heal. That is
// right at boot and wrong on a request path: node/queue.go measured what it
// costs, which is every caller queued behind the first one and an invocation
// that timed out.
func TestAGroupThatHasNotFormedIsRefusedAndNeverWaitedOn(t *testing.T) {
	opening := make(chan struct{})
	cluster := notLeading()

	opened := 0
	var mu sync.Mutex
	n, _, _ := makeNode(t, Config{Services: []string{Register, Worker, Stats}},
		WithRaft(func(string) (raft.Cluster, error) {
			<-opening
			mu.Lock()
			opened++
			mu.Unlock()
			return cluster, nil
		}))
	d := &databases{node: n}

	// THE FIRST ASK ANSWERS IMMEDIATELY, which is the whole claim.
	done := make(chan error, 1)
	go func() {
		_, err := d.groupFor("/tau/databases/v1/shop/prod/kv/QmLedger")
		done <- err
	}()
	select {
	case err := <-done:
		require.ErrorIs(t, err, databaseService.ErrGroupForming)
	case <-time.After(5 * time.Second):
		t.Fatal("a caller waited on a group that was still forming")
	}

	close(opening)
	require.Eventually(t, func() bool {
		got, err := d.groupFor("/tau/databases/v1/shop/prod/kv/QmLedger")
		return err == nil && got == cluster
	}, 10*time.Second, 10*time.Millisecond)

	// ONE GROUP PER DATABASE AND ONE OPEN PER GROUP: a second ask joins the
	// first rather than starting an election beside it.
	for range 5 {
		d.groupFor("/tau/databases/v1/shop/prod/kv/QmLedger")
	}
	mu.Lock()
	require.Equal(t, 1, opened)
	mu.Unlock()

	// AND CLOSING THE NODE'S DATABASES CLOSES THE GROUPS IT OPENED. A cluster
	// left running keeps a protocol handler, a heartbeat and a log open on a
	// peer nobody is using.
	require.NoError(t, d.Close())
	_, shut := cluster.did()
	require.True(t, shut)
}

// TestAGROUPTHATCOULDNOTBEOPENEDSAYSSOANDISTRIEDAGAIN.
//
// It says so rather than looking like one that has not finished forming — an
// operator retrying forever is the failure a named refusal exists to stop. And
// it is tried again, because raft.New spends thirty seconds on whatever the
// disk, the host and the network were doing at the time: a failure left in the
// table would make one bad minute permanent for that ledger on this node.
func TestAGroupThatCouldNotBeOpenedSaysSoAndIsTriedAgain(t *testing.T) {
	var mu sync.Mutex
	opens := 0

	n, _, _ := makeNode(t, Config{Services: []string{Register, Worker, Stats}},
		WithRaft(func(string) (raft.Cluster, error) {
			mu.Lock()
			opens++
			mu.Unlock()
			return nil, errors.New("no disk")
		}))
	d := &databases{node: n}

	require.Eventually(t, func() bool {
		_, err := d.groupFor("/tau/databases/v1/shop/prod/kv/QmLedger")
		return err != nil && !errors.Is(err, databaseService.ErrGroupForming) &&
			strings.Contains(err.Error(), "no disk")
	}, 10*time.Second, 10*time.Millisecond, "the reason the group could not be opened never reached a caller")

	// AND EVERY ASK AFTER A FAILURE IS A FRESH ATTEMPT rather than the same
	// answer read back out of the table.
	require.Eventually(t, func() bool {
		d.groupFor("/tau/databases/v1/shop/prod/kv/QmLedger")
		mu.Lock()
		defer mu.Unlock()
		return opens > 1
	}, 10*time.Second, 10*time.Millisecond, "a group that failed to open was never tried again")

	require.NoError(t, d.Close())
}

// TestANODEONITSWAYOUTOPENSNOMOREGROUPS.
//
// closeGroups empties the table, and a request already past the resident lookup
// would otherwise open a fresh one afterwards — a live voter with a protocol
// handler, a heartbeat and a vote in a quorum, that nothing will ever close
// again. A node that has shut down would leave it behind.
func TestANodeOnItsWayOutOpensNoMoreGroups(t *testing.T) {
	var mu sync.Mutex
	opens := 0

	cluster := notLeading()
	n, _, _ := makeNode(t, Config{Services: []string{Register, Worker, Stats}},
		WithRaft(func(string) (raft.Cluster, error) {
			mu.Lock()
			opens++
			mu.Unlock()
			return cluster, nil
		}))
	d := &databases{node: n}

	require.Eventually(t, func() bool {
		_, err := d.groupFor("/tau/databases/v1/shop/prod/kv/QmLedger")
		return err == nil
	}, 10*time.Second, 10*time.Millisecond)
	require.NoError(t, d.Close())

	// AND THE REFUSAL IS NOT "STILL FORMING", which is the distinction the
	// caller acts on: told that, it retries here for a group that will never
	// form; told this, it asks another voter.
	_, err := d.groupFor("/tau/databases/v1/shop/prod/kv/QmOther")
	require.ErrorIs(t, err, databaseService.ErrGroupClosed)
	require.NotErrorIs(t, err, databaseService.ErrGroupForming)
	require.ErrorIs(t, err, databaseService.ErrNotHere,
		"a node on its way out did not send the caller anywhere else")

	mu.Lock()
	require.Equal(t, 1, opens, "a node that had shut down opened another raft group")
	mu.Unlock()
}

// TestAMISSFROMAGROUPTHATISGONEISNOTAMISS.
//
// A closed cluster and a nil state machine both answer empty, and the kv engine
// turns that into "no such key" — which is a fact a ledger's reader acts on.
// The barrier proves leadership BEFORE the read; this is the window after it.
func TestAMissFromAGroupThatIsGoneIsNotAMiss(t *testing.T) {
	cluster := notLeading()
	cluster.leader = true
	store := over(cluster)

	// A leader that has the key answers it, and one that has not answers a
	// miss: both of those are the ledger talking.
	require.NoError(t, store.Put(t.Context(), "entry-1", []byte("one")))
	_, held, err := store.Get(t.Context(), "never-written")
	require.NoError(t, err)
	require.False(t, held)

	// A NODE THAT NO LONGER LEADS DID NOT ANSWER A MISS — IT DID NOT ANSWER.
	// leading() and the barrier both ran while it still led.
	cluster.emptyAfterBarrier = true

	_, _, err = store.Get(t.Context(), "entry-1")
	require.ErrorIs(t, err, databaseService.ErrNotLeading,
		"a group that went away between the barrier and the read answered an empty ledger")

	keys, err := store.List(t.Context(), "")
	require.ErrorIs(t, err, databaseService.ErrNotLeading)
	require.Nil(t, keys)
}

// TestACANCELLEDCALLERDOESNOTPAYTHEFULLBUDGET. pkg/raft takes a duration and
// not a context, so a caller that has already given up would otherwise hold a
// commit open for the whole of raftOperation — on a request path where the
// gateway timed out seconds ago.
func TestACancelledCallerDoesNotPayTheFullBudget(t *testing.T) {
	cluster := notLeading()
	cluster.leader = true
	store := over(cluster)

	gone, stop := context.WithCancel(t.Context())
	stop()
	require.ErrorIs(t, store.Put(gone, "entry-1", []byte("one")), context.Canceled)
	_, _, err := store.Get(gone, "entry-1")
	require.ErrorIs(t, err, context.Canceled)

	// AND A DEADLINE SHORTER THAN THE STORE'S IS THE ONE THAT COUNTS.
	soon, done := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer done()
	require.NoError(t, store.Put(soon, "entry-2", []byte("two")))

	budget, err := store.budget(soon)
	require.NoError(t, err)
	require.Less(t, budget, store.timeout, "the caller's deadline was not what bounded the call")
}

// TestANODEWITHNOWHERETOOPENAGROUPKEEPSNOLEDGER, which is what makes the
// service's refusal by name true: the composer either handed this node a way to
// open a group or it did not.
func TestANodeWithNowhereToOpenAGroupKeepsNoLedger(t *testing.T) {
	// BUILT WITHOUT THE HARNESS, which always hands a node a raft opener: what
	// is under test here is the composition that has none.
	d := &databases{node: &Node{opts: options{}}}

	_, err := d.storesFor(t.Context(), ledgerSpec())
	require.ErrorIs(t, err, ErrNoRaft)

	_, err = d.groupFor("/tau/databases/v1/shop/prod/kv/QmLedger")
	require.ErrorIs(t, err, ErrNoRaft)
}

// TestTHEGROUPANDTHESTOREAGREEONTHENAME, by construction rather than by two
// expressions that happen to match — which is the rule storeName already keeps
// for a crdt store and its holdings.
func TestTheGroupAndTheStoreAgreeOnTheNameADatabaseHas(t *testing.T) {
	spec := ledgerSpec()

	name, err := groupName(spec)
	require.NoError(t, err)
	require.Equal(t, raftDomain+"/shop/prod/kv/QmLedger", name)

	store, err := storeName(spec)
	require.NoError(t, err)
	require.Equal(t, databaseStore+"/shop/prod/kv/QmLedger", store)

	// AND A SPEC THAT ADDRESSES NOTHING NAMES NOTHING. A separator in a segment
	// is a readable path into somebody else's ledger.
	_, err = groupName(database.Spec{Type: database.KV, Name: "ledger"})
	require.ErrorIs(t, err, database.ErrUnaddressable)
}

// TestWHICHSTOREADATABASEGETSISITSOWNDECLARATION, and not a second engine: the
// schema declares ONE engine for tau's own, with a consensus beside it.
func TestTheConsensusPicksTheStoreAndNotASecondEngine(t *testing.T) {
	cluster := notLeading()
	cluster.leader = true

	n, _, _ := makeNode(t, Config{Services: []string{Register, Worker, Stats}},
		WithRaft(func(string) (raft.Cluster, error) { return cluster, nil }))
	d := &databases{node: n}
	t.Cleanup(func() { d.Close() })

	crdt, err := d.storesFor(t.Context(), database.Spec{
		Type: database.KV, Engine: database.Tau, Consensus: database.CRDT,
		Project: "shop", Environment: "prod", Name: "QmSessions",
	})
	require.NoError(t, err)
	require.IsType(t, cacheStore{}, crdt)

	require.Eventually(t, func() bool {
		ledger, err := d.storesFor(t.Context(), ledgerSpec())
		if err != nil {
			return false
		}
		return ledger.Put(t.Context(), "entry-1", []byte("one")) == nil
	}, 10*time.Second, 10*time.Millisecond, "the ledger never reached its group")

	// AND A DATABASE THAT DECLARED NOTHING IS A CRDT ONE, which is what every
	// caller that predates the field keeps getting.
	silent, err := d.storesFor(t.Context(), database.Spec{
		Type: database.KV, Engine: database.Tau,
		Project: "shop", Environment: "prod", Name: "QmSessions",
	})
	require.NoError(t, err)
	require.IsType(t, cacheStore{}, silent)
}

// TestALEDGERISNOTKEPTBYTHISNODESOWNENGINES.
//
// These engines are the fallback for a node that runs no database service and
// can reach none. A ledger has nothing to keep its order in there — no group,
// no owner, nowhere to send a write this node may not make — so it is refused
// BY NAME. Serving it as crdt would resolve two entries into one with no error
// to anybody.
func TestALedgerIsNotKeptByThisNodesOwnEngines(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Register, Worker, Stats}})
	d := &databases{node: n}

	grant := kvGrantFor("shop/prod/QmLedger")
	grant.Settings["kv/consensus"] = database.Raft

	_, err := d.OpenKV(t.Context(), grant)
	require.ErrorIs(t, err, ErrConsensusUnserved)
	require.ErrorContains(t, err, "raft")

	// AND THE OTHER HALF OF THE AXIS IS UNCHANGED, which is the point of
	// reading the word rather than guessing from what is registered.
	crdt := kvGrantFor("shop/prod/QmSessions")
	crdt.Settings["kv/consensus"] = database.CRDT
	_, err = d.OpenKV(t.Context(), crdt)
	require.NoError(t, err)
}

func ledgerSpec() database.Spec {
	return database.Spec{
		Type: database.KV, Engine: database.Tau, Consensus: database.Raft,
		Project: "shop", Environment: "prod", Name: "QmLedger",
	}
}
