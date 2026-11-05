package jobs

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/p2p/rpc"
	plmv2 "github.com/taubyte/tau/pkg/proto/gen/tau/plm/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/plm/v2/plmv2connect"
	"github.com/taubyte/tau/services/builder"
	"github.com/taubyte/tau/services/plm"
	"github.com/taubyte/tau/services/plm/clients"
	"github.com/taubyte/tau/services/plm/run"
)

// dispatchHarness is a plm node served on a mocknet, handing every job it claims
// to builders on peers of their own, each serving its runs over the builder's
// protocol. Its claim tick never fires in a test; a job queued or a builder done is
// what moves it, or a test's step.
type dispatchHarness struct {
	*harness
	mn       mocknet.Mocknet
	Jobs     plmv2connect.JobServiceClient
	builders *dialled
}

// dialled is the builders a dispatch harness knows, reached over their protocol
// from the plm node's host.
type dialled struct {
	from host.Host

	mu  sync.Mutex
	ids []peer.ID
	at  map[peer.ID]*clients.Builder
}

func (d *dialled) Peers() []peer.ID {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]peer.ID(nil), d.ids...)
}

func (d *dialled) At(id peer.ID) (run.Builder, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if c, ok := d.at[id]; ok {
		return c, nil
	}
	c, err := clients.BuilderAt(d.from, id)
	if err != nil {
		return nil, err
	}
	d.at[id] = c
	return c, nil
}

func (d *dialled) add(id peer.ID) {
	d.mu.Lock()
	d.ids = append(d.ids, id)
	d.mu.Unlock()
}

func (d *dialled) remove(id peer.ID) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for i, known := range d.ids {
		if known == id {
			d.ids = append(d.ids[:i], d.ids[i+1:]...)
			return
		}
	}
}

func newDispatchHarness(t *testing.T) *dispatchHarness {
	t.Helper()
	mn := newMocknet(t)
	self := newHost(t, mn)
	builders := &dialled{from: self, at: map[peer.ID]*clients.Builder{}}
	store, records, repos, clock := newStore(), newRecords(t), newRepositories(testRepo), newClock()
	s, err := newServer(Config{ClaimInterval: time.Hour, ReconcileEvery: time.Hour, RecordReadInterval: 10 * time.Millisecond},
		withHost(self), withStore(store), withRecords(records), withRepositories(repos), withClock(clock.now), withBuilders(builders))
	require.NoError(t, err)
	require.NoError(t, s.serve(t.Context()))
	t.Cleanup(func() { s.Close() })

	caller := newHost(t, mn)
	c, err := rpc.NewClient(caller, self.ID(), plm.Protocol)
	require.NoError(t, err)
	t.Cleanup(c.Close)
	return &dispatchHarness{
		harness:  &harness{server: s, store: store, records: records, repos: repos, clock: clock, host: self},
		mn:       mn,
		Jobs:     plmv2connect.NewJobServiceClient(c.HTTP(), c.URL()),
		builders: builders,
	}
}

// aBuilder is a builder on its own peer, serving runs its test drives: each plan and
// plm waits for the test to say how it goes.
type aBuilder struct {
	host   host.Host
	listen *rpc.Server
	runs   *builder.Runs
	assets *fakeAssets

	// handed is each run as it starts, and how it is to go is said on its turn.
	handed chan handed
}

// handed is one run in the builder's hands: what it was given, and where the test
// says how it goes.
type handed struct {
	work run.Work
	ctx  context.Context
	out  io.Writer
	turn chan turn
}

// turn is how a run goes: what it writes, what it keeps, and how it ends.
type turn struct {
	write        string
	keep         string
	environments []string
	err          error
	done         bool
}

// driven is a builder's executor, each run handed to the test.
type driven struct{ r *aBuilder }

func (d driven) Plan(ctx context.Context, w run.Work, out io.Writer) ([]string, error) {
	t := d.wait(ctx, w, out)
	return t.environments, t.err
}

func (d driven) Run(ctx context.Context, w run.Work, out run.Output) error {
	return d.wait(ctx, w, out).err
}

func (d driven) wait(ctx context.Context, w run.Work, out io.Writer) turn {
	h := handed{work: w, ctx: ctx, out: out, turn: make(chan turn)}
	d.r.handed <- h
	for t := range h.turn {
		if t.write != "" {
			io.WriteString(out, t.write)
		}
		if t.keep != "" {
			if o, ok := out.(run.Output); ok {
				o.Asset(ctx, t.keep, stringReader(t.keep))
			}
		}
		if t.done {
			return t
		}
	}
	return turn{err: errors.New("the test let go of the run")}
}

func stringReader(s string) io.Reader { return &onceReader{s: s} }

// onceReader reads s once.
type onceReader struct{ s string }

func (r *onceReader) Read(p []byte) (int, error) {
	if r.s == "" {
		return 0, io.EOF
	}
	n := copy(p, r.s)
	r.s = r.s[n:]
	return n, nil
}

// connect is a builder joining: serving on its peer, and named to the plm node.
func (h *dispatchHarness) connect(t *testing.T) *aBuilder {
	t.Helper()
	at := newHost(t, h.mn)
	r := &aBuilder{host: at, assets: newAssets(), handed: make(chan handed)}
	runs, err := builder.NewRuns(driven{r}, nil, r.assets, stamped{h.clock}, builder.Logs{Dir: t.TempDir()})
	require.NoError(t, err)
	listen, err := rpc.Listen(t.Context(), at, plm.BuilderProtocol, builder.Handler(runs))
	require.NoError(t, err)
	r.runs, r.listen = runs, listen
	t.Cleanup(func() {
		listen.Close()
		runs.Close()
	})
	h.builders.add(at.ID())
	// A BUILDER JOINING is found on the claim loop's next tick, which never comes in
	// a test: it is woken instead.
	h.busy.wake.ring()
	return r
}

// next is the next run the builder is handed by the plm node's claim loop.
func (h *dispatchHarness) next(t *testing.T, r *aBuilder) handed {
	t.Helper()
	select {
	case got := <-r.handed:
		return got
	case <-time.After(10 * time.Second):
		t.Fatal("the builder was handed nothing")
		return handed{}
	}
}

// idle returns once no builder holds a run of this node's: what was handed out has
// been settled.
func (h *dispatchHarness) idle(t *testing.T) {
	t.Helper()
	require.Eventually(t, func() bool {
		h.busy.mu.Lock()
		defer h.busy.mu.Unlock()
		return len(h.busy.held) == 0
	}, 10*time.Second, time.Millisecond)
}

// leave is the builder gone, as a builder whose process ends is: its host, and every
// connection of it, closed.
func (h *dispatchHarness) leave(r *aBuilder) {
	h.builders.remove(r.host.ID())
	r.listen.Close()
	r.host.Close()
}

func (r handed) say(t turn) { r.turn <- t }

// following is a follow of a job's first run, attached: it has read the run's first
// event, so whatever the builder says after reaches it.
type following struct {
	stream *connect.ServerStreamForClient[plmv2.Job_Event]
}

func (h *dispatchHarness) follow(t *testing.T, id string) *following {
	t.Helper()
	stream, err := h.Jobs.Follow(t.Context(), connect.NewRequest(&plmv2.FollowRequest{JobId: id, Attempt: 1}))
	require.NoError(t, err)
	require.True(t, stream.Receive(), "the run's first event: %v", stream.Err())
	return &following{stream: stream}
}

// end is the status the run ended in and the lines it wrote, read to the stream's end.
func (f *following) end(t *testing.T) (plmv2.JobStatus, []string) {
	t.Helper()
	var (
		last  plmv2.JobStatus
		lines []string
	)
	for f.stream.Receive() {
		switch k := f.stream.Msg().GetKind().(type) {
		case *plmv2.Job_Event_Status:
			last = k.Status
		case *plmv2.Job_Event_Message:
			lines = append(lines, k.Message)
		}
	}
	require.NoError(t, f.stream.Err())
	return last, lines
}

func TestAJobIsHandedToABuilderAndWhatItProducedComesBack(t *testing.T) {
	h := newDispatchHarness(t)
	r := h.connect(t)

	plan := h.push(t, testRef, testCommit)
	planning := h.next(t, r)
	require.Equal(t, plan, planning.work.JobID)
	require.Equal(t, testRepo, planning.work.RepositoryKey)
	require.Equal(t, testCommit, planning.work.CommitSHA)
	require.Empty(t, planning.work.Environment, "a push is a plan first")
	planning.say(turn{write: "main feeds prod\n", environments: []string{testEnvironment}, done: true})

	building := h.next(t, r)
	require.Equal(t, testEnvironment, building.work.Environment, "the environment job the plan queued is handed to the builder freed by it")
	require.Equal(t, uint32(1), building.work.Attempt)
	require.Equal(t, "main feeds prod\n", building.work.Prelude, "what the plan said opens the build's log")
	require.Equal(t, r.host.ID().String(), h.mustRecord(t, building.work.JobID).Builder, "the record names the builder the run went to")
	followed := h.follow(t, building.work.JobID)
	building.say(turn{write: "compiling\n", keep: "QmResource"})
	building.say(turn{done: true})

	status, lines := followed.end(t)
	require.Equal(t, plmv2.JobStatus_JobStatusSuccess, status, "the follow, proxied to the builder, ends with how the job was settled")
	require.Contains(t, lines, "compiling")
	require.Contains(t, lines, "main feeds prod")
	h.idle(t)
	rec := h.mustRecord(t, building.work.JobID)
	require.Equal(t, assetCID([]byte("QmResource")), rec.Assets[assetKey(testEnvironment, "QmResource")],
		"an artifact a builder kept is the job's")
	require.NotEmpty(t, rec.Logs, "the log the builder kept is filed on the job")
	cid, err := h.Built("project-of-"+testRepo, testEnvironment, "QmResource")
	require.NoError(t, err)
	require.Equal(t, assetCID([]byte("QmResource")), cid)
}

func TestABuildersFailureIsTheJobs(t *testing.T) {
	h := newDispatchHarness(t)
	r := h.connect(t)
	h.push(t, testRef, testCommit)
	h.next(t, r).say(turn{environments: []string{testEnvironment}, done: true})

	building := h.next(t, r)
	followed := h.follow(t, building.work.JobID)
	building.say(turn{err: errors.New("the linker gave up"), done: true})
	status, lines := followed.end(t)
	require.Equal(t, plmv2.JobStatus_JobStatusFailed, status)
	require.Contains(t, lines, "error: the linker gave up", "the builder's words are the log's last line")
}

func TestABuilderThatGoesAwayLeavesItsJobToTheNext(t *testing.T) {
	h := newDispatchHarness(t)
	first := h.connect(t)
	h.push(t, testRef, testCommit)
	h.next(t, first).say(turn{environments: []string{testEnvironment}, done: true})

	building := h.next(t, first)
	h.leave(first)
	h.idle(t)
	require.Equal(t, plmv2.JobStatus_JobStatusClaimed, h.status(t, building.work.JobID),
		"a builder gone is not a failed build: the job waits out its claim")
	close(building.turn)

	// THE CLAIM LAPSES, and the queue hands the job to whoever is free.
	h.clock.advance(2 * h.cfg.Queue.VisibilityTimeout)
	second := h.connect(t)
	again := h.next(t, second)
	require.Equal(t, building.work.JobID, again.work.JobID)
	require.Equal(t, uint32(2), again.work.Attempt, "the next run of the same job")
	again.say(turn{done: true})
	h.idle(t)
	require.Equal(t, plmv2.JobStatus_JobStatusSuccess, h.status(t, building.work.JobID))
}

func TestACancelIsSaidToTheBuilder(t *testing.T) {
	h := newDispatchHarness(t)
	r := h.connect(t)
	h.push(t, testRef, testCommit)
	h.next(t, r).say(turn{environments: []string{testEnvironment}, done: true})

	building := h.next(t, r)
	followed := h.follow(t, building.work.JobID)
	_, err := h.Jobs.Cancel(t.Context(), connect.NewRequest(&plmv2.JobRef{Id: building.work.JobID}))
	require.NoError(t, err)
	<-building.ctx.Done()
	building.say(turn{write: "stopping\n", err: building.ctx.Err(), done: true})

	status, lines := followed.end(t)
	require.Equal(t, plmv2.JobStatus_JobStatusCancelled, status, "the builder stopped the run, and the log ends as the job was left")
	require.Contains(t, lines, "stopping")
}

func TestABuilderRunningSomethingTurnsAnotherRunDown(t *testing.T) {
	h := newDispatchHarness(t)
	r := h.connect(t)
	h.push(t, testRef, testCommit)
	planning := h.next(t, r)

	c, err := clients.BuilderAt(h.host, r.host.ID())
	require.NoError(t, err)
	t.Cleanup(func() { c.Close() })
	_, err = c.Build(t.Context(), run.Work{JobID: "another", Attempt: 1, Environment: testEnvironment})
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(err), "one run at a time")
	require.ErrorContains(t, err, builder.ErrBusy.Error())

	planning.say(turn{environments: []string{testEnvironment}, done: true})
}

func TestABuilderThatCannotBeReachedCostsTheRunNotTheJob(t *testing.T) {
	h := newDispatchHarness(t)
	h.builders.add(newHost(t, h.mn).ID())
	plan := h.push(t, testRef, testCommit)
	require.Eventually(t, func() bool { return h.mustRecord(t, plan).Builder != "" }, 10*time.Second, time.Millisecond, "the job was handed out")
	h.idle(t)
	require.Equal(t, plmv2.JobStatus_JobStatusClaimed, h.status(t, plan), "abandoned: the claim lapses and the queue hands it on")
}

func TestABuilderIsOneThatIsFree(t *testing.T) {
	var b busy
	id := peer.ID("a builder")
	require.True(t, b.take(id))
	require.False(t, b.take(id), "a builder holding a run is not free")
	b.release(id)
	rung := b.wake.wait()
	require.True(t, b.take(id))
	b.give(id)
	<-rung
}
