package jobs

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/libp2p/go-libp2p/core/host"
	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/p2p/rpc"
	buildv2 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/build/v2/buildv2connect"
	"github.com/taubyte/tau/services/build"
)

// dispatchHarness is a build node with no runner of its own, served on a mocknet:
// every job it claims is handed to a runner that opened a stream to it. Its claim
// tick never fires in a test; a runner joining or finishing is what moves it.
type dispatchHarness struct {
	*harness
	mn     mocknet.Mocknet
	Jobs   buildv2connect.JobServiceClient
	assets *fakeAssets
}

func newDispatchHarness(t *testing.T) *dispatchHarness {
	t.Helper()
	mn := newMocknet(t)
	self := newHost(t, mn)
	store, records, repos, clock, assets := newStore(), newRecords(t), newRepositories(testRepo), newClock(), newAssets()
	s, err := newServer(Config{ClaimInterval: time.Hour, ReconcileEvery: time.Hour},
		withHost(self), withStore(store), withRecords(records), withRepositories(repos), withClock(clock.now), withAssets(assets))
	require.NoError(t, err)
	require.NoError(t, s.serve(t.Context()))
	t.Cleanup(func() { s.Close() })

	caller := newHost(t, mn)
	c, err := rpc.NewClient(caller, self.ID(), build.Protocol)
	require.NoError(t, err)
	t.Cleanup(c.Close)
	return &dispatchHarness{
		harness: &harness{server: s, store: store, records: records, repos: repos, clock: clock, host: self},
		mn:      mn,
		Jobs:    buildv2connect.NewJobServiceClient(c.HTTP(), c.URL()),
		assets:  assets,
	}
}

// aRunner is a runner on its own peer, its stream open and said ready: the build
// node has it free once connect returns.
type aRunner struct {
	stream *connect.BidiStreamForClient[buildv2.RunnerReport, buildv2.Assignment]
	cancel context.CancelFunc
	host   host.Host
}

// leave is the runner gone, as a runner whose process ends is: its host, and every
// connection of it, closed.
func (r *aRunner) leave() {
	r.cancel()
	r.host.Close()
}

func (h *dispatchHarness) connect(t *testing.T) *aRunner {
	t.Helper()
	at := newHost(t, h.mn)
	c, err := rpc.NewClient(at, h.host.ID(), build.Protocol)
	require.NoError(t, err)
	t.Cleanup(c.Close)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	stream := buildv2connect.NewRunnerServiceClient(c.HTTP(), c.URL()).Serve(ctx)

	joined := h.runners.wake.wait()
	require.NoError(t, stream.Send(&buildv2.RunnerReport{Kind: &buildv2.RunnerReport_Ready{Ready: &buildv2.Empty{}}}))
	<-joined
	return &aRunner{stream: stream, cancel: cancel, host: at}
}

func (r *aRunner) next(t *testing.T) *buildv2.Assignment {
	t.Helper()
	a, err := r.stream.Receive()
	require.NoError(t, err)
	return a
}

func (r *aRunner) say(t *testing.T, report *buildv2.RunnerReport) {
	t.Helper()
	require.NoError(t, r.stream.Send(report))
}

func (r *aRunner) wrote(t *testing.T, text string) {
	r.say(t, &buildv2.RunnerReport{Kind: &buildv2.RunnerReport_Output{Output: []byte(text)}})
}

func (r *aRunner) finish(t *testing.T, failure string, environments ...string) {
	r.say(t, &buildv2.RunnerReport{Kind: &buildv2.RunnerReport_Finished{Finished: &buildv2.Finished{Error: failure, Environments: environments}}})
}

// following is a follow of a job's first run, attached: it has read the run's first
// event, so whatever the runner says after reaches it. It waits on the job and on
// nothing else.
type following struct {
	stream *connect.ServerStreamForClient[buildv2.Job_Event]
}

func (h *dispatchHarness) follow(t *testing.T, id string) *following {
	t.Helper()
	stream, err := h.Jobs.Follow(t.Context(), connect.NewRequest(&buildv2.FollowRequest{JobId: id, Attempt: 1}))
	require.NoError(t, err)
	require.True(t, stream.Receive(), "the run's first event: %v", stream.Err())
	return &following{stream: stream}
}

// end is the status the run ended in and the lines it wrote, read to the stream's end.
func (f *following) end(t *testing.T) (buildv2.JobStatus, []string) {
	t.Helper()
	var (
		last  buildv2.JobStatus
		lines []string
	)
	for f.stream.Receive() {
		switch k := f.stream.Msg().GetKind().(type) {
		case *buildv2.Job_Event_Status:
			last = k.Status
		case *buildv2.Job_Event_Message:
			lines = append(lines, k.Message)
		}
	}
	require.NoError(t, f.stream.Err())
	return last, lines
}

func TestAJobIsHandedToARunnerAndWhatItProducedComesBack(t *testing.T) {
	h := newDispatchHarness(t)
	r := h.connect(t)

	plan := h.push(t, testRef, testCommit)
	planning := r.next(t).GetPlan()
	require.NotNil(t, planning, "a push is a plan first")
	require.Equal(t, plan, planning.GetJobId())
	require.Equal(t, testRepo, planning.GetRepositoryKey())
	require.Equal(t, testCommit, planning.GetCommitSha())
	require.Empty(t, planning.GetEnvironment())
	r.wrote(t, "main feeds prod\n")
	r.finish(t, "", testEnvironment)

	building := r.next(t).GetBuild()
	require.NotNil(t, building, "the environment job the plan queued is handed to the runner freed by it")
	require.Equal(t, testEnvironment, building.GetEnvironment())
	require.Equal(t, uint32(1), building.GetAttempt())
	followed := h.follow(t, building.GetJobId())
	r.wrote(t, "compiling\n")
	r.say(t, &buildv2.RunnerReport{Kind: &buildv2.RunnerReport_Stored{Stored: &buildv2.Stored{ResourceId: "QmResource", Cid: "bafyartifact", Bytes: 42}}})
	r.finish(t, "")

	status, lines := followed.end(t)
	require.Equal(t, buildv2.JobStatus_JobStatusSuccess, status)
	require.Contains(t, lines, "compiling")
	require.Equal(t, "bafyartifact", h.mustRecord(t, building.GetJobId()).Assets[assetKey(testEnvironment, "QmResource")],
		"an artifact a runner kept is the job's as if this node had stored it")
	cid, err := h.Built("project-of-"+testRepo, testEnvironment, "QmResource")
	require.NoError(t, err)
	require.Equal(t, "bafyartifact", cid)
}

func TestARunnersFailureIsTheJobs(t *testing.T) {
	h := newDispatchHarness(t)
	r := h.connect(t)
	h.push(t, testRef, testCommit)
	r.next(t)
	r.finish(t, "", testEnvironment)

	building := r.next(t).GetBuild()
	followed := h.follow(t, building.GetJobId())
	r.finish(t, "the linker gave up")
	status, lines := followed.end(t)
	require.Equal(t, buildv2.JobStatus_JobStatusFailed, status)
	require.Contains(t, lines, "error: the linker gave up", "the runner's words are the log's last line")
}

func TestARunnerThatGoesAwayLeavesItsJobToTheNext(t *testing.T) {
	h := newDispatchHarness(t)
	first := h.connect(t)
	h.push(t, testRef, testCommit)
	first.next(t)
	first.finish(t, "", testEnvironment)

	building := first.next(t).GetBuild()
	running := h.dispatchedRuns()
	first.leave()
	<-running
	require.Equal(t, buildv2.JobStatus_JobStatusClaimed, h.status(t, building.GetJobId()),
		"a runner gone is not a failed build: the job waits out its claim")

	// THE CLAIM LAPSES, and the queue hands the job to whoever is free.
	h.clock.advance(2 * h.cfg.Queue.VisibilityTimeout)
	second := h.connect(t)
	again := second.next(t).GetBuild()
	require.NotNil(t, again)
	require.Equal(t, building.GetJobId(), again.GetJobId())
	require.Equal(t, uint32(2), again.GetAttempt(), "the next run of the same job")
	second.finish(t, "")
}

func TestACancelIsSaidToTheRunner(t *testing.T) {
	h := newDispatchHarness(t)
	r := h.connect(t)
	h.push(t, testRef, testCommit)
	r.next(t)
	r.finish(t, "", testEnvironment)

	building := r.next(t).GetBuild()
	followed := h.follow(t, building.GetJobId())
	_, err := h.Jobs.Cancel(t.Context(), connect.NewRequest(&buildv2.JobRef{Id: building.GetJobId()}))
	require.NoError(t, err)
	require.NotNil(t, r.next(t).GetCancel(), "the runner is told to stop")
	r.wrote(t, "stopping\n")
	r.finish(t, "context canceled")

	status, _ := followed.end(t)
	require.Equal(t, buildv2.JobStatus_JobStatusCancelled, status)
}

func TestOnlyALeaderTakesARunnerAndOnlyOneThatSaysItIsReady(t *testing.T) {
	h := newDispatchHarness(t)
	at := newHost(t, h.mn)
	c, err := rpc.NewClient(at, h.host.ID(), build.Protocol)
	require.NoError(t, err)
	t.Cleanup(c.Close)
	client := buildv2connect.NewRunnerServiceClient(c.HTTP(), c.URL())

	silent := client.Serve(t.Context())
	require.NoError(t, silent.Send(&buildv2.RunnerReport{Kind: &buildv2.RunnerReport_Output{Output: []byte("hello")}}))
	_, err = silent.Receive()
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err), "a runner says it is ready first")

	h.store.stepDown()
	follower := client.Serve(t.Context())
	// A FOLLOWER ANSWERS BEFORE IT READS, so the send can meet a stream it has ended,
	// which connect says with io.EOF; the answer is what Receive reads.
	if err := follower.Send(&buildv2.RunnerReport{Kind: &buildv2.RunnerReport_Ready{Ready: &buildv2.Empty{}}}); err != nil {
		require.ErrorIs(t, err, io.EOF)
	}
	_, err = follower.Receive()
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(err), "a follower sends a runner to the leader")
	require.True(t, strings.Contains(err.Error(), "leader"))
}

func TestARunnerGoneBeforeItIsHandedAnythingIsNotHandedAnything(t *testing.T) {
	h := newDispatchHarness(t)
	r := h.connect(t)
	r.leave()
	_, err := r.stream.Receive()
	require.Error(t, err)

	// The stream ends on the server when its reader sees the close; the runner
	// stays free in the list until then and is skipped once it has.
	for st, ok := h.runners.take(); ok; st, ok = h.runners.take() {
		<-st.gone
	}
	_, ok := h.runners.take()
	require.False(t, ok)
	ended := &stream{gone: make(chan struct{})}
	ended.end()
	ended.end()
	require.False(t, h.runners.putBack(ended), "an ended stream is not put back, and ending twice is ending once")
}

// dispatchedRuns is closed once every run handed to a runner so far has ended.
func (h *dispatchHarness) dispatchedRuns() <-chan struct{} {
	done := make(chan struct{})
	go func() {
		h.dispatched.Wait()
		close(done)
	}()
	return done
}
