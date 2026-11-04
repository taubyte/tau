package backend

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	buildv2 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v2"
	"github.com/taubyte/tau/services/build/run"
)

func enqueue(t *testing.T, h *harness, key, ref, commit string) (string, error) {
	t.Helper()
	return h.enqueue(t.Context(), &buildv2.PushEvent{RepositoryKey: key, Ref: ref, CommitSha: commit})
}

func TestEnqueueWritesTheCreatedEventAndBothRowsInOneDelta(t *testing.T) {
	h := newHarness(t)

	var duringBatch int
	h.records.onBatch(func() {
		// Nothing of this job may be visible before the delta commits.
		duringBatch = len(h.rows(t))
	})

	id, err := enqueue(t, h, testRepo, testRef, testCommit)
	require.NoError(t, err)
	require.Equal(t, 0, duringBatch, "no row may land before the one entry that writes them all")

	rec := h.mustRecord(t, id)
	require.Equal(t, buildv2.JobStatus_JobStatusOpen, rec.status())
	require.Equal(t, testRepo, rec.RepositoryKey)
	require.Equal(t, testRef, rec.Ref)
	require.Equal(t, testCommit, rec.CommitSHA)
	require.Equal(t, "project-of-"+testRepo, rec.ProjectID)
	require.Equal(t, h.clock.now().Unix(), rec.CreatedAt)
	require.Zero(t, rec.Attempt)

	require.Len(t, h.records.keys(t, projectRows(rec.ProjectID)), 1)
	require.Len(t, h.records.keys(t, openRows(testRepo)), 1)

	ready, claimed := h.work.Depth()
	require.Equal(t, 1, ready)
	require.Equal(t, 0, claimed)
}

func TestARedeliveredPushIsTheSameJob(t *testing.T) {
	h := newHarness(t)

	first, err := enqueue(t, h, testRepo, testRef, testCommit)
	require.NoError(t, err)
	second, err := enqueue(t, h, testRepo, testRef, testCommit)
	require.NoError(t, err)

	require.Equal(t, first, second)
	ready, _ := h.work.Depth()
	require.Equal(t, 1, ready, "a redelivery must not make a second item")
	require.Len(t, h.records.keys(t, projectRows("project-of-"+testRepo)), 1)
}

func TestARedeliveryOfAClaimedOrFinishedJobChangesNothing(t *testing.T) {
	for _, status := range []buildv2.JobStatus{
		buildv2.JobStatus_JobStatusClaimed,
		buildv2.JobStatus_JobStatusSuccess,
		buildv2.JobStatus_JobStatusFailed,
		buildv2.JobStatus_JobStatusCancelled,
	} {
		t.Run(status.String(), func(t *testing.T) {
			h := newHarness(t)
			id := h.push(t, testRef, testCommit)

			// Drain the item so a re-push would be visible.
			d, err := h.work.Claim()
			require.NoError(t, err)
			require.NoError(t, h.work.Ack(d))

			require.NoError(t, h.finishNow(t, id, status, ""))

			again, err := enqueue(t, h, testRepo, testRef, testCommit)
			require.NoError(t, err)
			require.Equal(t, id, again)

			ready, claimed := h.work.Depth()
			require.Equal(t, 0, ready)
			require.Equal(t, 0, claimed)
		})
	}
}

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

func TestEnqueueRefusesStrangerAuthoredNonsense(t *testing.T) {
	h := newHarness(t)

	type testCase struct{ key, ref, commit string }
	for name, tc := range map[string]testCase{
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

	require.Empty(t, h.rows(t), "nothing refused may have written a row")
}

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
	require.Empty(t, h.rows(t))
}

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

func TestAdmissionBoundsTheQueueAndTheRepository(t *testing.T) {
	t.Run("queue depth", func(t *testing.T) {
		h := newHarnessCfg(t, func(c *Config) { c.MaxQueueDepth = 1 })

		h.push(t, testRef, testCommit)
		_, err := enqueue(t, h, testRepo, "refs/heads/other", testCommit)
		require.Equal(t, connect.CodeResourceExhausted, connect.CodeOf(err))
	})

	t.Run("open per repository", func(t *testing.T) {
		h := newHarnessCfg(t, func(c *Config) { c.MaxOpenPerRepo = 1 })

		h.push(t, testRef, testCommit)
		_, err := enqueue(t, h, testRepo, "refs/heads/other", testCommit)
		require.Equal(t, connect.CodeResourceExhausted, connect.CodeOf(err))
	})

	// The bound counts OUTSTANDING work, not history: a finished job's open row
	// is gone, so it does not hold a repository's slot forever.
	t.Run("a finished job frees the slot", func(t *testing.T) {
		h := newHarnessCfg(t, func(c *Config) { c.MaxOpenPerRepo = 1 })

		id := h.push(t, testRef, testCommit)
		require.NoError(t, h.finishNow(t, id, buildv2.JobStatus_JobStatusSuccess, ""))

		_, err := enqueue(t, h, testRepo, "refs/heads/other", testCommit)
		require.NoError(t, err)
	})
}

func TestAStoreThatRefusesTheWriteIsUnavailable(t *testing.T) {
	h := newHarness(t)
	h.records.failBatches(errors.New("the store is full"))

	_, err := enqueue(t, h, testRepo, testRef, testCommit)
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(err))
}

func TestAPushThatFailsAfterTheRecordIsReported(t *testing.T) {
	h := newHarness(t)

	// The created event goes to kvdb; refuse the queue's entry.
	h.store.failBatches(errors.New("log is full"))

	_, err := enqueue(t, h, testRepo, testRef, testCommit)
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(err))

	h.store.failBatches(nil)
	require.Len(t, h.records.keys(t, openRoot()), 1, "the job survives, for reconcile to find")
}

func TestACorruptRecordIsInternalRatherThanASecondJob(t *testing.T) {
	h := newHarness(t)
	key, _ := parseRepositoryKey(testRepo)
	id := jobID(key, testRef, testCommit)
	h.records.garble(t, id)

	_, err := enqueue(t, h, testRepo, testRef, testCommit)
	require.Equal(t, connect.CodeInternal, connect.CodeOf(err))
}

func TestTheOnlyWritesAreOneEntrysRows(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	require.NoError(t, h.cancel(id))

	entries := h.records.entriesAbout(id)
	require.Len(t, entries, 2, "two deltas: created, then cancelled")
	events := h.records.keys(t, eventPrefix(id))
	require.Len(t, events, 2, "and each is one event of its own")
	opened := openRowsOf(testRepo, id) + strings.TrimPrefix(events[0], eventPrefix(id))

	require.Equal(t, []string{
		"set " + events[0],
		"set " + projectRow("project-of-"+testRepo, id),
		"set " + opened,
	}, entries[0], "create composes the event, the project row and the open row")

	require.Equal(t, []string{
		"set " + events[1],
		"del " + opened,
		"set " + terminalRow(h.clock.now(), id),
	}, entries[1], "and a cancel the event, the open row removed and the retention row")
}

func TestTheRepairPushCanFailOnItsOwn(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	d, err := h.work.Claim()
	require.NoError(t, err)
	require.NoError(t, h.work.Ack(d))

	h.store.failBatches(errors.New("log is full"))

	_, err = enqueue(t, h, testRepo, testRef, testCommit)
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(err))
	require.Equal(t, buildv2.JobStatus_JobStatusOpen, h.status(t, id))
}

// gatedRepositories blocks the first Project call, the only place Enqueue waits on
// another node, so a second delivery can run while the first is in flight.
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

func TestARedeliveryThatOverlapsTheFirstDeliveryDoesNotBuildTwice(t *testing.T) {
	gate := newGatedRepositories(testRepo)
	h := newHarness(t, withRepositories(gate))

	type answer struct {
		id  string
		err error
	}
	overlapped := make(chan answer, 1)
	go func() {
		id, err := h.enqueue(context.Background(), &buildv2.PushEvent{
			RepositoryKey: testRepo, Ref: testRef, CommitSha: testCommit,
		})
		overlapped <- answer{id, err}
	}()

	// The first delivery is now inside the round trip, past whatever it read.
	<-gate.entered

	id, err := enqueue(t, h, testRepo, testRef, testCommit)
	require.NoError(t, err)
	require.True(t, h.mustStep(t), "the second delivery's job must run")
	require.Equal(t, buildv2.JobStatus_JobStatusSuccess, h.status(t, id))
	require.True(t, h.mustStep(t), "and the environment job it planned")

	close(gate.release)
	got := <-overlapped
	require.NoError(t, got.err)
	require.Equal(t, id, got.id, "both deliveries name one job")

	rec := h.mustRecord(t, id)
	require.Equal(t, buildv2.JobStatus_JobStatusSuccess, rec.status(),
		"a finished job must not be reopened by a delivery that read the table before it finished")
	require.Equal(t, uint32(1), rec.Attempt, "the attempt counter must not be rewound")

	ready, claimed := h.work.Depth()
	require.Zero(t, ready+claimed, "the overlapping delivery must not mint a second item")

	require.False(t, h.mustStep(t), "there is nothing left to claim")
	require.Len(t, h.runner.seen(), 1, "one push, one build")
}

// A DELIVERY THAT WAS AWAY decides on the job another node finished meanwhile:
// its section reads after the round trip, so the events that landed during it are
// what it decides on.
func TestACreateDecidesOnWhatLandedWhileItWasAway(t *testing.T) {
	gate := newGatedRepositories(testRepo)
	h := newHarness(t, withRepositories(gate))

	key, ok := parseRepositoryKey(testRepo)
	require.True(t, ok)
	id := jobID(key, testRef, testCommit)

	done := make(chan error, 1)
	go func() {
		_, err := h.enqueue(context.Background(), &buildv2.PushEvent{
			RepositoryKey: testRepo, Ref: testRef, CommitSha: testCommit,
		})
		done <- err
	}()
	<-gate.entered

	h.plantRecord(t, jobRecord{
		ID:            id,
		Status:        int32(buildv2.JobStatus_JobStatusSuccess),
		RepositoryKey: testRepo,
		Ref:           testRef,
		CommitSHA:     testCommit,
		ProjectID:     "project-of-" + testRepo,
		CreatedAt:     h.clock.now().Unix(),
		Attempt:       1,
	})

	close(gate.release)
	require.NoError(t, <-done)

	require.Equal(t, buildv2.JobStatus_JobStatusSuccess, h.status(t, id),
		"a job another node finished must not be reopened by the delivery that was away")
	ready, claimed := h.work.Depth()
	require.Zero(t, ready+claimed, "and it must not be queued here either")
}

// AND ONE THAT LANDS AFTER IT DECIDED is a second created, which the fold ignores:
// the other node's ending applies to the job this node created, and the item this
// node queued is dropped by begin, unbuilt.
func TestACreateThatRacedAnotherNodesIsOneJob(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	h.elsewhere(t, id, &buildv2.JobEvent{Kind: &buildv2.JobEvent_Created{Created: &buildv2.JobCreated{
		RepositoryKey: testRepo, Ref: testRef, CommitSha: testCommit, ProjectId: "project-of-" + testRepo,
	}}})
	h.elsewhere(t, id, &buildv2.JobEvent{Kind: &buildv2.JobEvent_Claimed{Claimed: &buildv2.JobClaimed{Attempt: 1}}})
	h.elsewhere(t, id, &buildv2.JobEvent{Kind: &buildv2.JobEvent_Settled{Settled: &buildv2.JobSettled{
		Attempt: 1, Status: buildv2.JobStatus_JobStatusSuccess,
	}}})
	require.Equal(t, buildv2.JobStatus_JobStatusSuccess, h.status(t, id))

	require.True(t, h.mustStep(t), "the item this node queued is claimed")
	require.Empty(t, h.runner.planned, "and dropped, since the job had ended")
	require.Equal(t, buildv2.JobStatus_JobStatusSuccess, h.status(t, id))
}

func TestASecondDeliveryCannotDecideWhileTheFirstIsWriting(t *testing.T) {
	h := newHarness(t)

	writing := make(chan struct{})
	trying := make(chan struct{})
	h.records.onBatch(func() {
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
		second, secondErr = h.enqueue(context.Background(), &buildv2.PushEvent{
			RepositoryKey: testRepo, Ref: testRef, CommitSha: testCommit,
		})
	}()

	first, err := enqueue(t, h, testRepo, testRef, testCommit)
	require.NoError(t, err)
	<-done

	require.NoError(t, secondErr)
	require.Equal(t, first, second, "both deliveries name one job")
	require.Len(t, h.records.entriesAbout(first), 1, "two overlapping deliveries, one created event")

	ready, claimed := h.work.Depth()
	require.Equal(t, 1, ready, "and one item")
	require.Zero(t, claimed)
}

func TestWhatADeliveryFindsWhenItComesBackFromAuth(t *testing.T) {
	// A leadership flap during the round trip. The Batch would refuse it
	// anyway; refusing before it is what keeps the answer "ask somebody else"
	// rather than a store error.
	t.Run("leadership is gone", func(t *testing.T) {
		gate := newGatedRepositories(testRepo)
		h := newHarness(t, withRepositories(gate))

		done := make(chan error, 1)
		go func() {
			_, err := h.enqueue(context.Background(), &buildv2.PushEvent{
				RepositoryKey: testRepo, Ref: testRef, CommitSha: testCommit,
			})
			done <- err
		}()

		<-gate.entered
		h.store.stepDown()
		close(gate.release)

		require.Equal(t, connect.CodeUnavailable, connect.CodeOf(<-done))
		require.Empty(t, h.rows(t), "a follower writes nothing")
	})

	// An event that will not decode is Internal on the way back for the same
	// reason it is on the way in: minting a second job over it would lose
	// whatever the first one was.
	t.Run("the job is there and will not decode", func(t *testing.T) {
		gate := newGatedRepositories(testRepo)
		h := newHarness(t, withRepositories(gate))

		key, ok := parseRepositoryKey(testRepo)
		require.True(t, ok)

		done := make(chan error, 1)
		go func() {
			_, err := h.enqueue(context.Background(), &buildv2.PushEvent{
				RepositoryKey: testRepo, Ref: testRef, CommitSha: testCommit,
			})
			done <- err
		}()

		<-gate.entered
		h.records.garble(t, jobID(key, testRef, testCommit))
		close(gate.release)

		require.Equal(t, connect.CodeInternal, connect.CodeOf(<-done))
		ready, _ := h.work.Depth()
		require.Zero(t, ready)
	})
}

type confirmer struct {
	err   error
	asked int
}

func (c *confirmer) Confirm(_ context.Context, key, ref, commit string) error {
	c.asked++
	return c.err
}

func TestEnqueueConfirmsThePushAtTheRepository(t *testing.T) {
	type testCase struct {
		name string
		err  error
		code connect.Code
	}
	for _, tc := range []testCase{
		{"not at the commit", fmt.Errorf("%w: moved on", run.ErrNotAtCommit), connect.CodeFailedPrecondition},
		{"forge unreachable", errors.New("dial"), connect.CodeUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &confirmer{err: tc.err}
			h := newHarness(t, withConfirmer(c))
			_, err := enqueue(t, h, testRepo, testRef, testCommit)
			require.Equal(t, tc.code, connect.CodeOf(err))
			require.Equal(t, 1, c.asked)
			require.Empty(t, h.rows(t), "nothing refused may have written a row")
		})
	}

	c := &confirmer{}
	h := newHarness(t, withConfirmer(c))
	id := h.push(t, testRef, testCommit)
	require.Equal(t, id, h.push(t, testRef, testCommit))
	require.Equal(t, 1, c.asked, "a push already queued is not asked about again")

	_, err := newServer(Config{}, withConfirmer(nil))
	require.Error(t, err)
}
