package backend

import (
	"errors"
	"strings"
	"testing"

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
