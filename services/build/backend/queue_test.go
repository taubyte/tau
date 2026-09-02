package backend

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/raft"
	buildv1 "github.com/taubyte/tau/pkg/v2/proto/gen/tau/build/v1"
)

func enqueue(t *testing.T, h *harness, key, ref, commit string) (string, error) {
	t.Helper()
	return h.enqueue(t.Context(), &buildv1.PushEvent{RepositoryKey: key, Ref: ref, CommitSha: commit})
}

// One Enqueue writes four things in a fixed order: the record, the project
// index row and the open index row in ONE entry, then the queue item. A reader
// never sees a job that is in one of the three and not the others.
func TestEnqueueWritesTheRecordAndBothIndexesInOneEntry(t *testing.T) {
	h := newHarness(t)

	var duringBatch int
	h.store.onBatch(func() {
		// Nothing of this job may be visible before the entry commits.
		duringBatch = len(h.store.Keys(rootSegment + "/"))
	})

	id, err := enqueue(t, h, testRepo, testRef, testCommit)
	require.NoError(t, err)
	require.Equal(t, 0, duringBatch, "no row may land before the one entry that writes them all")

	rec := h.mustRecord(t, id)
	require.Equal(t, buildv1.JobStatus_JobStatusOpen, rec.status())
	require.Equal(t, testRepo, rec.RepositoryKey)
	require.Equal(t, testRef, rec.Ref)
	require.Equal(t, testCommit, rec.CommitSHA)
	require.Equal(t, "project-of-"+testRepo, rec.ProjectID)
	require.Equal(t, h.clock.now().Unix(), rec.CreatedAt)
	require.Zero(t, rec.Attempt)

	require.Len(t, h.store.Keys(projectIndexPrefix(rec.ProjectID)), 1)
	require.Len(t, h.store.Keys(openIndexPrefix(testRepo)), 1)

	ready, claimed := h.work.Depth()
	require.Equal(t, 1, ready)
	require.Equal(t, 0, claimed)
}

// IDEMPOTENCY IS THE JOB ID. There is no dedup index for the two to disagree
// about, and a redelivered webhook is answered with the job it already made.
func TestARedeliveredPushIsTheSameJob(t *testing.T) {
	h := newHarness(t)

	first, err := enqueue(t, h, testRepo, testRef, testCommit)
	require.NoError(t, err)
	second, err := enqueue(t, h, testRepo, testRef, testCommit)
	require.NoError(t, err)

	require.Equal(t, first, second)
	ready, _ := h.work.Depth()
	require.Equal(t, 1, ready, "a redelivery must not make a second item")
	require.Len(t, h.store.Keys(projectIndexPrefix("project-of-"+testRepo)), 1)
}

// A redelivery of a job somebody is running must not push it again, and one of
// a finished job must not resurrect it.
func TestARedeliveryOfAClaimedOrFinishedJobChangesNothing(t *testing.T) {
	for _, status := range []buildv1.JobStatus{
		buildv1.JobStatus_JobStatusClaimed,
		buildv1.JobStatus_JobStatusSuccess,
		buildv1.JobStatus_JobStatusFailed,
		buildv1.JobStatus_JobStatusCancelled,
	} {
		t.Run(status.String(), func(t *testing.T) {
			h := newHarness(t)
			id := h.push(t, testRef, testCommit)

			// Drain the item so a re-push would be visible.
			d, err := h.work.Claim()
			require.NoError(t, err)
			require.NoError(t, h.work.Ack(d))

			rec := h.mustRecord(t, id)
			require.NoError(t, h.finish(rec, status, ""))

			again, err := enqueue(t, h, testRepo, testRef, testCommit)
			require.NoError(t, err)
			require.Equal(t, id, again)

			ready, claimed := h.work.Depth()
			require.Equal(t, 0, ready)
			require.Equal(t, 0, claimed)
		})
	}
}

// The one crash window this design has: a record written and a push that never
// landed, because queue.Push does its own Batch and the two cannot be fused. A
// redelivered webhook repairs it, and so does reconcile.
func TestARedeliveryRepairsARecordWhosePushWasLost(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	// Lose the item without touching the record — the state a crash between the
	// two writes leaves behind.
	d, err := h.work.Claim()
	require.NoError(t, err)
	require.NoError(t, h.work.Ack(d))
	ready, _ := h.work.Depth()
	require.Equal(t, 0, ready)

	again, err := enqueue(t, h, testRepo, testRef, testCommit)
	require.NoError(t, err)
	require.Equal(t, id, again)

	ready, _ = h.work.Depth()
	require.Equal(t, 1, ready, "an Open record with no item must be re-queued")
}

// Every argument here originates in an unauthenticated POST, so this is the
// last place they are looked at before becoming a hash and three key segments.
func TestEnqueueRefusesStrangerAuthoredNonsense(t *testing.T) {
	h := newHarness(t)

	for name, tc := range map[string]struct{ key, ref, commit string }{
		"no repository":        {"", testRef, testCommit},
		"non-canonical key":    {"GitHub.com/Acme/Pay", testRef, testCommit},
		"key with a scheme":    {"https://github.com/acme/pay", testRef, testCommit},
		"a tag, not a branch":  {testRepo, "refs/tags/v1", testCommit},
		"a climbing ref":       {testRepo, "refs/heads/../../x", testCommit},
		"an empty ref segment": {testRepo, "refs/heads/a//b", testCommit},
		"a bare branch name":   {testRepo, "main", testCommit},
		"a short sha":          {testRepo, testRef, strings.Repeat("a", 39)},
		"an uppercase sha":     {testRepo, testRef, strings.ToUpper(testCommit)},
		"no sha":               {testRepo, testRef, ""},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := enqueue(t, h, tc.key, tc.ref, tc.commit)
			require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
		})
	}

	require.Empty(t, h.store.Keys(rootSegment+"/"), "nothing refused may have written a row")
}

// A follower answers "ask somebody else" to a write, which is the same code
// clients.Pool.Do walks past for a peer that is down.
func TestEnqueueOnAFollowerIsUnavailable(t *testing.T) {
	h := newHarness(t)
	h.store.stepDown()

	_, err := enqueue(t, h, testRepo, testRef, testCommit)
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(err))
	require.Empty(t, h.repos.asked, "a follower must not even ask auth")
}

func TestEnqueueOnALeaderThatCannotBarrierIsUnavailable(t *testing.T) {
	h := newHarness(t)
	h.store.failBarrier(errors.New("timed out"))

	_, err := enqueue(t, h, testRepo, testRef, testCommit)
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(err))
}

// A push for a repository nobody registered is refused BEFORE anything is
// queued — the admission check on stranger-authored input.
// LEADERSHIP IS CHECKED INSIDE THE SECTION, not only at the door. A delivery
// waits on jobLock while another transition holds it across a replicated entry,
// and leadership can be lost in that wait — so the barrier and the refusal are
// where the read is, which is the only place they say anything about the read.
func TestASectionRefusesLeadershipItLostWhileItWaited(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)
	h.store.stepDown()

	found, err := h.resume(id)
	require.False(t, found)
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(err))
}

func TestAPushForAnUnregisteredRepositoryIsRefusedBeforeAnythingIsQueued(t *testing.T) {
	h := newHarness(t)

	_, err := enqueue(t, h, "github.com/someone/else", testRef, testCommit)
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))

	ready, _ := h.work.Depth()
	require.Equal(t, 0, ready)
	require.Empty(t, h.store.Keys(rootSegment+"/"))
}

// "This cloud does not know that repository" and "auth is unreachable" are the
// same string and very different answers, so the code is preserved.
func TestAnUnreachableAuthIsUnavailableRatherThanARefusal(t *testing.T) {
	h := newHarness(t)
	h.repos.fail(connect.NewError(connect.CodeUnavailable, errors.New("no peer answered")))

	_, err := enqueue(t, h, testRepo, testRef, testCommit)
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(err))
}

func TestAuthsOwnRefusalsArePassedThrough(t *testing.T) {
	for _, code := range []connect.Code{
		connect.CodeInvalidArgument, connect.CodeFailedPrecondition, connect.CodePermissionDenied,
	} {
		t.Run(code.String(), func(t *testing.T) {
			h := newHarness(t)
			h.repos.fail(connect.NewError(code, errors.New("no")))

			_, err := enqueue(t, h, testRepo, testRef, testCommit)
			require.Equal(t, code, connect.CodeOf(err))
		})
	}
}

func TestARepositoryBelongingToNoProjectIsAFailedPrecondition(t *testing.T) {
	h := newHarness(t)
	h.repos.projects[testRepo] = ""

	_, err := enqueue(t, h, testRepo, testRef, testCommit)
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
}

// Admission control, on input a stranger authored. v1 has no bound of either
// kind, so a push loop is a queue nobody can drain.
func TestAdmissionBoundsTheQueueAndTheRepository(t *testing.T) {
	t.Run("queue depth", func(t *testing.T) {
		h := newHarness(t, func(s *Service) error { s.cfg.MaxQueueDepth = 1; return nil })

		h.push(t, testRef, testCommit)
		_, err := enqueue(t, h, testRepo, "refs/heads/other", testCommit)
		require.Equal(t, connect.CodeResourceExhausted, connect.CodeOf(err))
	})

	t.Run("open per repository", func(t *testing.T) {
		h := newHarness(t, func(s *Service) error { s.cfg.MaxOpenPerRepo = 1; return nil })

		h.push(t, testRef, testCommit)
		_, err := enqueue(t, h, testRepo, "refs/heads/other", testCommit)
		require.Equal(t, connect.CodeResourceExhausted, connect.CodeOf(err))
	})

	// The bound counts OUTSTANDING work, not history: a finished job's open row
	// is gone, so it does not hold a repository's slot forever.
	t.Run("a finished job frees the slot", func(t *testing.T) {
		h := newHarness(t, func(s *Service) error { s.cfg.MaxOpenPerRepo = 1; return nil })

		id := h.push(t, testRef, testCommit)
		require.NoError(t, h.finish(h.mustRecord(t, id), buildv1.JobStatus_JobStatusSuccess, ""))

		_, err := enqueue(t, h, testRepo, "refs/heads/other", testCommit)
		require.NoError(t, err)
	})
}

func TestAStoreThatRefusesTheWriteIsUnavailable(t *testing.T) {
	h := newHarness(t)
	h.store.failBatches(errors.New("log is full"))

	_, err := enqueue(t, h, testRepo, testRef, testCommit)
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(err))
}

// The push is a separate entry from the record, so it can fail on its own — and
// when it does the caller is told, rather than being handed an id for a job
// nothing will run until reconcile notices.
func TestAPushThatFailsAfterTheRecordIsReported(t *testing.T) {
	h := newHarness(t)

	// Let the record's entry through, then refuse the queue's.
	h.store.onBatch(func() { h.store.failBatches(errors.New("log is full")) })

	_, err := enqueue(t, h, testRepo, testRef, testCommit)
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(err))

	h.store.failBatches(nil)
	require.Len(t, h.store.Keys(openIndexRoot()), 1, "the record survives, for reconcile to find")
}

func TestACorruptRecordIsInternalRatherThanASecondJob(t *testing.T) {
	h := newHarness(t)
	key, _ := parseRepositoryKey(testRepo)
	id := jobID(key, testRef, testCommit)
	h.store.put(t, jobRecordKey(id), []byte("not cbor"))

	_, err := enqueue(t, h, testRepo, testRef, testCommit)
	require.Equal(t, connect.CodeInternal, connect.CodeOf(err))
}

// set and del are the only two shapes of write this package makes, and Store
// deliberately offers no single-key Set or Delete: a single-key write is a way
// to write half a transition.
func TestTheOnlyWritesAreBatchOps(t *testing.T) {
	require.NotNil(t, set("k", nil).Set)
	require.Nil(t, set("k", nil).Delete)
	require.NotNil(t, del("k").Delete)
	require.Nil(t, del("k").Set)

	var _ []raft.BatchOp = []raft.BatchOp{set("a", nil), del("b")}
}

// The repair path's push is its own entry too.
func TestTheRepairPushCanFailOnItsOwn(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	d, err := h.work.Claim()
	require.NoError(t, err)
	require.NoError(t, h.work.Ack(d))

	h.store.failBatches(errors.New("log is full"))

	_, err = enqueue(t, h, testRepo, testRef, testCommit)
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(err))
	require.Equal(t, buildv1.JobStatus_JobStatusOpen, h.status(t, id))
}

// gatedRepositories is auth with a hold on the one question this service asks
// it. Project is a p2p round trip — the only place an Enqueue waits on somebody
// else — so it is where the state an Enqueue read can go stale under it. The
// FIRST call blocks in it; every later one answers normally, so the test can
// drive a second delivery all the way through while the first is still in
// flight.
type gatedRepositories struct {
	*fakeRepositories

	held    atomic.Bool
	entered chan struct{}
	release chan struct{}
}

func newGatedRepositories(keys ...string) *gatedRepositories {
	return &gatedRepositories{
		fakeRepositories: newRepositories(keys...),
		entered:          make(chan struct{}),
		release:          make(chan struct{}),
	}
}

func (g *gatedRepositories) Project(ctx context.Context, key string) (string, error) {
	if g.held.CompareAndSwap(false, true) {
		close(g.entered)
		<-g.release
	}
	return g.fakeRepositories.Project(ctx, key)
}

// A REDELIVERY THAT OVERLAPS THE FIRST DELIVERY IS THE CASE THE IDEMPOTENCY
// CLAIM HAS TO SURVIVE. GitHub redelivers a webhook whose response it did not
// see, which is exactly the response that is still being computed — so the two
// deliveries are CONCURRENT, not sequential, and every sequential test above
// passes either way.
//
// The whole of Enqueue's idempotency is a read of the job table. Between that
// read and the write it decides, there is a round trip to auth; a delivery that
// reads an empty table before the round trip and writes after it writes what it
// decided on a table that is no longer there. What that costs is not a wasted
// entry: the record goes back to Open with its attempt counter at zero, and the
// push mints a second queue item because the ack of the first run had removed
// the queue's own record of the id — so one push is built twice.
func TestARedeliveryThatOverlapsTheFirstDeliveryDoesNotBuildTwice(t *testing.T) {
	gate := newGatedRepositories(testRepo)
	h := newHarness(t, func(s *Service) error { s.repos = gate; return nil })

	type answer struct {
		id  string
		err error
	}
	overlapped := make(chan answer, 1)
	go func() {
		id, err := h.enqueue(context.Background(), &buildv1.PushEvent{
			RepositoryKey: testRepo, Ref: testRef, CommitSha: testCommit,
		})
		overlapped <- answer{id, err}
	}()

	// The first delivery is now inside the round trip, past whatever it read.
	<-gate.entered

	id, err := enqueue(t, h, testRepo, testRef, testCommit)
	require.NoError(t, err)
	require.True(t, h.mustStep(t), "the second delivery's job must run")
	require.Equal(t, buildv1.JobStatus_JobStatusSuccess, h.status(t, id))

	close(gate.release)
	got := <-overlapped
	require.NoError(t, got.err)
	require.Equal(t, id, got.id, "both deliveries name one job")

	rec := h.mustRecord(t, id)
	require.Equal(t, buildv1.JobStatus_JobStatusSuccess, rec.status(),
		"a finished job must not be reopened by a delivery that read the table before it finished")
	require.Equal(t, uint32(1), rec.Attempt, "the attempt counter must not be rewound")

	ready, claimed := h.work.Depth()
	require.Zero(t, ready+claimed, "the overlapping delivery must not mint a second item")

	require.False(t, h.mustStep(t), "there is nothing left to claim")
	require.Len(t, h.runner.seen(), 1, "one push, one build")
}

// staleStore is a node that has just won an election: it holds entries that are
// committed and NOT YET APPLIED, so what it reads locally lags until a barrier.
// raft.MockCluster cannot lag — its Barrier does nothing — so the one thing a
// barrier is for is invisible without this.
type staleStore struct {
	*fakeStore

	mu        sync.Mutex
	unapplied []raft.BatchOp
}

// commitElsewhere is an entry another leader wrote and this node has accepted
// and not yet applied.
func (s *staleStore) commitElsewhere(ops ...raft.BatchOp) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.unapplied = append(s.unapplied, ops...)
}

func (s *staleStore) Barrier(timeout time.Duration) error {
	if err := s.fakeStore.Barrier(timeout); err != nil {
		return err
	}
	s.mu.Lock()
	ops := s.unapplied
	s.unapplied = nil
	s.mu.Unlock()
	if len(ops) == 0 {
		return nil
	}
	return s.fakeStore.MockCluster.Batch(ops, timeout)
}

// A DELIVERY COMING BACK FROM AUTH MAY HAVE MISSED A TERM. Another node can
// have taken leadership during the round trip, built the push, finished it and
// handed leadership back; the entries that say so are committed here and not
// applied, so a local read does not see them. That is why the write is decided
// behind a barrier and not on what the delivery read on its way in — the same
// argument pkg/v2/queue makes before it hands out an item.
func TestACreateDecidesOnWhatABarrierMakesVisible(t *testing.T) {
	gate := newGatedRepositories(testRepo)
	var store *staleStore
	h := newHarness(t, func(s *Service) error {
		store = &staleStore{fakeStore: s.store.(*fakeStore)}
		s.store = store
		s.repos = gate
		return nil
	})

	key, ok := parseRepositoryKey(testRepo)
	require.True(t, ok)
	id := jobID(key, testRef, testCommit)

	done := make(chan error, 1)
	go func() {
		_, err := h.enqueue(context.Background(), &buildv1.PushEvent{
			RepositoryKey: testRepo, Ref: testRef, CommitSha: testCommit,
		})
		done <- err
	}()
	<-gate.entered

	finished := jobRecord{
		ID:            id,
		Status:        int32(buildv1.JobStatus_JobStatusSuccess),
		RepositoryKey: testRepo,
		Ref:           testRef,
		CommitSHA:     testCommit,
		ProjectID:     "project-of-" + testRepo,
		CreatedAt:     h.clock.now().Unix(),
		UpdatedAt:     h.clock.now().Unix(),
		Attempt:       1,
	}
	value, err := finished.encode()
	require.NoError(t, err)
	store.commitElsewhere(
		set(jobRecordKey(id), value),
		set(projectIndexKey(finished.ProjectID, id), nil),
		set(terminalIndexKey(h.clock.now(), id), nil),
	)
	_, visible := store.Get(jobRecordKey(id))
	require.False(t, visible, "an unapplied entry must not be readable, or this test proves nothing")

	close(gate.release)
	require.NoError(t, <-done)

	require.Equal(t, buildv1.JobStatus_JobStatusSuccess, h.status(t, id),
		"a job another node finished must not be reopened by the delivery that was away")
	ready, claimed := h.work.Depth()
	require.Zero(t, ready+claimed, "and it must not be queued here either")
}

// recordingStore counts the entries that write a job record, which is how a
// test says "two deliveries, one write".
type recordingStore struct {
	*fakeStore

	mu      sync.Mutex
	records int
}

func (s *recordingStore) Batch(ops []raft.BatchOp, timeout time.Duration) error {
	if err := s.fakeStore.Batch(ops, timeout); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, op := range ops {
		if op.Set != nil && strings.HasSuffix(op.Set.Key, "/"+recordSegment) {
			s.records++
		}
	}
	return nil
}

func (s *recordingStore) written() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.records
}

// The interleaving one field over: a second delivery reads the job table while
// the first is BETWEEN its read and its write. Re-reading is not enough on its
// own — both deliveries would read the same empty table and both would write —
// so the read and the write it decides are one section, and the second delivery
// is parked outside it.
//
// The hook holds the first delivery inside its entry and waits on a deadline
// rather than on a signal from the second, because when this is correct the
// second delivery is parked and has no signal to send. That is the whole point,
// and it is pkg/v2/queue/exclusive_test.go's shape for the same reason.
func TestASecondDeliveryCannotDecideWhileTheFirstIsWriting(t *testing.T) {
	var store *recordingStore
	h := newHarness(t, func(s *Service) error {
		store = &recordingStore{fakeStore: s.store.(*fakeStore)}
		s.store = store
		return nil
	})

	writing := make(chan struct{})
	trying := make(chan struct{})
	h.store.onBatch(func() {
		close(writing)
		select {
		case <-trying:
			// The second delivery is in enqueue. Give it time to read.
			time.Sleep(100 * time.Millisecond)
		case <-time.After(time.Second):
			t.Error("the second delivery never started")
		}
	})

	var (
		second    string
		secondErr error
		done      = make(chan struct{})
	)
	go func() {
		defer close(done)
		<-writing
		close(trying)
		second, secondErr = h.enqueue(context.Background(), &buildv1.PushEvent{
			RepositoryKey: testRepo, Ref: testRef, CommitSha: testCommit,
		})
	}()

	first, err := enqueue(t, h, testRepo, testRef, testCommit)
	require.NoError(t, err)
	<-done

	require.NoError(t, secondErr)
	require.Equal(t, first, second, "both deliveries name one job")
	require.Equal(t, 1, store.written(), "two overlapping deliveries, one record write")

	ready, claimed := h.work.Depth()
	require.Equal(t, 1, ready, "and one item")
	require.Zero(t, claimed)
}

// THE DECISION IS REMADE WHEN THE DELIVERY COMES BACK, so everything the state
// of this node can have become while it was away is answered here rather than
// written through.
func TestWhatADeliveryFindsWhenItComesBackFromAuth(t *testing.T) {
	// A leadership flap during the round trip. The Batch would refuse it
	// anyway; refusing before it is what keeps the answer "ask somebody else"
	// rather than a store error.
	t.Run("leadership is gone", func(t *testing.T) {
		gate := newGatedRepositories(testRepo)
		h := newHarness(t, func(s *Service) error { s.repos = gate; return nil })

		done := make(chan error, 1)
		go func() {
			_, err := h.enqueue(context.Background(), &buildv1.PushEvent{
				RepositoryKey: testRepo, Ref: testRef, CommitSha: testCommit,
			})
			done <- err
		}()

		<-gate.entered
		h.store.stepDown()
		close(gate.release)

		require.Equal(t, connect.CodeUnavailable, connect.CodeOf(<-done))
		require.Empty(t, h.store.Keys(rootSegment+"/"), "a follower writes nothing")
	})

	// A row that will not decode is Internal on the way back for the same
	// reason it is on the way in: minting a second job over it would lose
	// whatever the first one was.
	t.Run("the record is there and will not decode", func(t *testing.T) {
		gate := newGatedRepositories(testRepo)
		h := newHarness(t, func(s *Service) error { s.repos = gate; return nil })

		key, ok := parseRepositoryKey(testRepo)
		require.True(t, ok)

		done := make(chan error, 1)
		go func() {
			_, err := h.enqueue(context.Background(), &buildv1.PushEvent{
				RepositoryKey: testRepo, Ref: testRef, CommitSha: testCommit,
			})
			done <- err
		}()

		<-gate.entered
		h.store.put(t, jobRecordKey(jobID(key, testRef, testCommit)), []byte("not cbor"))
		close(gate.release)

		require.Equal(t, connect.CodeInternal, connect.CodeOf(<-done))
		ready, _ := h.work.Depth()
		require.Zero(t, ready)
	})
}
