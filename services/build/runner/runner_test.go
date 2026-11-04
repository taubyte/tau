package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"github.com/libp2p/go-libp2p/core/host"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"
	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/proto/codec"
	buildv2 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/build/v2/buildv2connect"
	"github.com/taubyte/tau/services/build"
	"github.com/taubyte/tau/services/build/run"
	"github.com/taubyte/tau/services/storage"
)

// buildNode is a build node as a runner sees it: it takes the runner's stream, or
// sends it on as a follower does, and hands it what a test says.
type buildNode struct {
	host     host.Host
	refuse   bool
	sessions chan *session
}

// session is one runner's stream as the build node holds it. A send after its
// handler has returned would write to a stream connect closed, so it is refused.
type session struct {
	send    func(*buildv2.Assignment) error
	reports chan *buildv2.RunnerReport
	ended   chan error

	mu   sync.Mutex
	shut bool
}

func (s *session) assign(a *buildv2.Assignment) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.shut {
		return errors.New("the stream has ended")
	}
	return s.send(a)
}

func (b *buildNode) Serve(ctx context.Context, stream *connect.BidiStream[buildv2.RunnerReport, buildv2.Assignment]) error {
	if b.refuse {
		return connect.NewError(connect.CodeUnavailable, errors.New("not the leader"))
	}
	first, err := stream.Receive()
	if err != nil {
		return err
	}
	if first.GetReady() == nil {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("ready first"))
	}
	s := &session{send: stream.Send, reports: make(chan *buildv2.RunnerReport, 64), ended: make(chan error, 1)}
	defer func() {
		s.mu.Lock()
		s.shut = true
		s.mu.Unlock()
	}()
	b.sessions <- s
	for {
		rep, err := stream.Receive()
		if err != nil {
			s.ended <- err
			return nil
		}
		s.reports <- rep
	}
}

func newBuildNode(t *testing.T, mn mocknet.Mocknet, refuse bool) *buildNode {
	t.Helper()
	h, err := mn.GenPeer()
	require.NoError(t, err)
	b := &buildNode{host: h, refuse: refuse, sessions: make(chan *session, 4)}
	mux := http.NewServeMux()
	mux.Handle(buildv2connect.NewRunnerServiceHandler(b, codec.Option()))
	srv, err := rpc.Listen(t.Context(), h, build.Protocol, mux)
	require.NoError(t, err)
	t.Cleanup(func() { srv.Close() })
	return b
}

// executor is a runner's own work, as a test writes it.
type executor struct {
	plan func(ctx context.Context, w run.Work, out io.Writer) ([]string, error)
	run  func(ctx context.Context, w run.Work, out run.Output) error
}

func (e executor) Plan(ctx context.Context, w run.Work, out io.Writer) ([]string, error) {
	return e.plan(ctx, w, out)
}

func (e executor) Run(ctx context.Context, w run.Work, out run.Output) error {
	return e.run(ctx, w, out)
}

// keeper is a storage service that keeps what it is handed.
type keeper struct {
	mu   sync.Mutex
	kept map[string]string
	err  error
	cid  string
}

func (k *keeper) Put(_ context.Context, at storage.Address, name string, _ storage.Object, content io.Reader) (storage.Version, error) {
	b, err := io.ReadAll(content)
	if err != nil {
		return storage.Version{}, err
	}
	if k.err != nil {
		return storage.Version{}, k.err
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.kept == nil {
		k.kept = map[string]string{}
	}
	k.kept[fmt.Sprintf("%v/%s", at, name)] = string(b)
	return storage.Version{Record: storage.VersionRecord{RootCID: k.cid}}, nil
}

type confirmer struct{ err error }

func (c confirmer) Confirm(context.Context, string, string, string) error { return c.err }

// serving is a runner offering itself to the build nodes named, until the test ends.
func serving(t *testing.T, mn mocknet.Mocknet, s *server, builders ...*buildNode) {
	t.Helper()
	self, err := mn.GenPeer()
	require.NoError(t, err)
	require.NoError(t, mn.LinkAll())
	require.NoError(t, mn.ConnectAllButSelf())
	s.self = self
	s.builders = func() []libp2ppeer.ID {
		ids := []libp2ppeer.ID{self.ID()}
		for _, b := range builders {
			ids = append(ids, b.host.ID())
		}
		return ids
	}
	ctx, stop := context.WithCancel(context.Background())
	s.stop, s.stopped = stop, make(chan struct{})
	go s.serve(ctx)
	t.Cleanup(func() { s.Close() })
}

func (s *session) next(t *testing.T) *buildv2.RunnerReport {
	t.Helper()
	select {
	case rep := <-s.reports:
		return rep
	case err := <-s.ended:
		t.Fatalf("the runner's stream ended: %v", err)
		return nil
	}
}

// finished reads to the end of the job in hand: what it wrote, what it kept, and
// how it ended.
func (s *session) finished(t *testing.T) (string, []*buildv2.Stored, *buildv2.Finished) {
	t.Helper()
	var (
		wrote  strings.Builder
		stored []*buildv2.Stored
	)
	for {
		switch k := s.next(t).GetKind().(type) {
		case *buildv2.RunnerReport_Output:
			wrote.Write(k.Output)
		case *buildv2.RunnerReport_Stored:
			stored = append(stored, k.Stored)
		case *buildv2.RunnerReport_Finished:
			return wrote.String(), stored, k.Finished
		}
	}
}

func work(environment string) *buildv2.Work {
	return &buildv2.Work{JobId: "job", RepositoryKey: "github.com/acme/app", Ref: "refs/heads/main",
		CommitSha: strings.Repeat("c", 40), ProjectId: "QmProject", Attempt: 1, Environment: environment}
}

func TestARunnerPlansAndBuildsWhatItIsHanded(t *testing.T) {
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	b := newBuildNode(t, mn, false)
	kept := &keeper{cid: "bafyartifact"}
	var planned run.Work
	s := &server{
		exec: executor{
			plan: func(_ context.Context, w run.Work, out io.Writer) ([]string, error) {
				planned = w
				fmt.Fprintln(out, "main feeds prod")
				return []string{"prod"}, nil
			},
			run: func(ctx context.Context, w run.Work, out run.Output) error {
				fmt.Fprintln(out, "compiling")
				return out.Asset(ctx, "QmResource", strings.NewReader("wasm!"))
			},
		},
		confirm: confirmer{},
		assets:  kept,
	}
	serving(t, mn, s, b)
	sess := <-b.sessions

	require.NoError(t, sess.assign(&buildv2.Assignment{Kind: &buildv2.Assignment_Plan{Plan: work("")}}))
	wrote, _, finished := sess.finished(t)
	require.Equal(t, "main feeds prod\n", wrote)
	require.Empty(t, finished.GetError())
	require.Equal(t, []string{"prod"}, finished.GetEnvironments())
	require.Equal(t, "github.com/acme/app", planned.RepositoryKey)
	require.Equal(t, uint32(1), planned.Attempt)

	require.NoError(t, sess.assign(&buildv2.Assignment{Kind: &buildv2.Assignment_Build{Build: work("prod")}}))
	wrote, stored, finished := sess.finished(t)
	require.Equal(t, "compiling\n", wrote)
	require.Empty(t, finished.GetError())
	require.Len(t, stored, 1)
	require.Equal(t, "QmResource", stored[0].GetResourceId())
	require.Equal(t, "bafyartifact", stored[0].GetCid())
	require.Equal(t, uint64(5), stored[0].GetBytes(), "the bytes it kept, counted where it kept them")
	require.Len(t, kept.kept, 1, "the artifact is in storage, put there by the runner")
}

func TestWhatARunnerCannotDoIsTheJobsFailure(t *testing.T) {
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	b := newBuildNode(t, mn, false)
	s := &server{
		exec: executor{
			plan: func(context.Context, run.Work, io.Writer) ([]string, error) {
				return nil, errors.New("no project here")
			},
			run: func(ctx context.Context, _ run.Work, out run.Output) error {
				return out.Asset(ctx, "QmResource", strings.NewReader("wasm!"))
			},
		},
		confirm: confirmer{err: run.ErrNotAtCommit},
		assets:  &keeper{err: errors.New("storage is full")},
	}
	serving(t, mn, s, b)
	sess := <-b.sessions

	require.NoError(t, sess.assign(&buildv2.Assignment{Kind: &buildv2.Assignment_Plan{Plan: work("")}}))
	_, _, finished := sess.finished(t)
	require.Contains(t, finished.GetError(), run.ErrNotAtCommit.Error(), "a push is built only for what the repository holds")

	require.NoError(t, sess.assign(&buildv2.Assignment{Kind: &buildv2.Assignment_Build{Build: work("prod")}}))
	_, _, finished = sess.finished(t)
	require.Contains(t, finished.GetError(), "storage is full")

	s.assets = &keeper{}
	require.NoError(t, sess.assign(&buildv2.Assignment{Kind: &buildv2.Assignment_Build{Build: work("prod")}}))
	_, _, finished = sess.finished(t)
	require.Contains(t, finished.GetError(), "named no cid")

	s.assets = nil
	require.NoError(t, sess.assign(&buildv2.Assignment{Kind: &buildv2.Assignment_Build{Build: work("prod")}}))
	_, _, finished = sess.finished(t)
	require.Contains(t, finished.GetError(), "no storage service")

	s.assets = &keeper{cid: "bafy"}
	require.NoError(t, sess.assign(&buildv2.Assignment{Kind: &buildv2.Assignment_Build{Build: work("")}}))
	_, _, finished = sess.finished(t)
	require.Contains(t, finished.GetError(), "a plan builds nothing", "no environment is nothing to keep an artifact under")
}

func TestACancelStopsTheJobInHand(t *testing.T) {
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	b := newBuildNode(t, mn, false)
	started := make(chan struct{})
	s := &server{exec: executor{run: func(ctx context.Context, _ run.Work, _ run.Output) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}}}
	serving(t, mn, s, b)
	sess := <-b.sessions

	require.NoError(t, sess.assign(&buildv2.Assignment{Kind: &buildv2.Assignment_Build{Build: work("prod")}}))
	<-started
	require.NoError(t, sess.assign(&buildv2.Assignment{Kind: &buildv2.Assignment_Cancel{Cancel: &buildv2.Empty{}}}))
	_, _, finished := sess.finished(t)
	require.Contains(t, finished.GetError(), context.Canceled.Error())

	require.NoError(t, sess.assign(&buildv2.Assignment{Kind: &buildv2.Assignment_Cancel{Cancel: &buildv2.Empty{}}}),
		"a cancel with nothing in hand stops nothing")
}

func TestARunnerServesTheBuildNodeThatTakesIt(t *testing.T) {
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	follower := newBuildNode(t, mn, true)
	leader := newBuildNode(t, mn, false)
	s := &server{exec: executor{}}
	serving(t, mn, s, follower, leader)
	<-leader.sessions
	require.Empty(t, follower.sessions, "a follower sends it on")
}

func TestARunnerHandedTwoJobsAtOnceEndsTheStream(t *testing.T) {
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	b := newBuildNode(t, mn, false)
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	s := &server{exec: executor{run: func(ctx context.Context, _ run.Work, _ run.Output) error {
		select {
		case <-block:
		case <-ctx.Done():
		}
		return ctx.Err()
	}}}
	serving(t, mn, s, b)
	sess := <-b.sessions
	require.NoError(t, sess.assign(&buildv2.Assignment{Kind: &buildv2.Assignment_Build{Build: work("prod")}}))
	require.NoError(t, sess.assign(&buildv2.Assignment{Kind: &buildv2.Assignment_Build{Build: work("prod")}}))
	_, _, finished := sess.finished(t)
	require.Error(t, <-sess.ended, "the stream ends")
	require.Contains(t, finished.GetError(), context.Canceled.Error(), "the job in hand is stopped as the stream ends")
	again := <-b.sessions
	require.NotNil(t, again, "and the runner offers itself again")

	require.NoError(t, again.assign(&buildv2.Assignment{}))
	<-b.sessions
}

func TestClosingARunnerAbandonsItsJob(t *testing.T) {
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	b := newBuildNode(t, mn, false)
	started := make(chan struct{})
	s := &server{exec: executor{run: func(ctx context.Context, _ run.Work, _ run.Output) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}}}
	serving(t, mn, s, b)
	sess := <-b.sessions
	require.NoError(t, sess.assign(&buildv2.Assignment{Kind: &buildv2.Assignment_Build{Build: work("prod")}}))
	<-started
	require.NoError(t, s.Close())
	require.NoError(t, s.Close(), "closing twice is closing once")
	require.ErrorContains(t, s.Start(t.Context(), lone(t, nil)), "already started")
}
