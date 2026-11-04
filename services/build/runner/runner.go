// Package runner is the private build service, N of them: it holds no state and
// runs what build hands it. A runner opens RunnerService.Serve to the build node
// leading the queue and is handed one job at a time down the stream; it sends back
// what the job wrote and the artifacts it kept, and how the job ended. It serves
// nothing and is never dialled: the side that runs a repository's code is never
// the side that is asked (.notes/v2/build-topology.md).
package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/libp2p/go-libp2p/core/host"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"

	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/proto/codec"
	buildv2 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/build/v2/buildv2connect"
	"github.com/taubyte/tau/services/build"
	"github.com/taubyte/tau/services/build/run"
	"github.com/taubyte/tau/services/storage"
)

// lookAgain is how long a runner that found no build node to serve waits before it
// asks the directory again. The directory is gossiped and changes on its own
// schedule; there is nothing to subscribe to.
const lookAgain = 250 * time.Millisecond

// Assets is where a runner keeps what a job built: the storage service.
type Assets interface {
	Put(ctx context.Context, at storage.Address, name string, o storage.Object, content io.Reader) (storage.Version, error)
}

// New is the runner service, unstarted. Start builds it from its instance.
func New() service.Service { return &server{} }

type server struct {
	exec    run.Runner
	confirm run.Confirmer
	assets  Assets
	self    host.Host
	// builders is the build nodes to offer this runner to, asked each time.
	builders func() []libp2ppeer.ID

	stop    context.CancelFunc
	stopped chan struct{}
	also    []io.Closer
}

// serve offers this runner to build nodes until ctx ends: the leader takes it, a
// follower sends it on, and a stream that ends is opened again.
func (s *server) serve(ctx context.Context) {
	defer close(s.stopped)
	for ctx.Err() == nil {
		for _, id := range s.builders() {
			if id == s.self.ID() || ctx.Err() != nil {
				continue
			}
			s.session(ctx, id)
		}
		select {
		case <-ctx.Done():
		case <-time.After(lookAgain):
		}
	}
}

// session is one stream to one build node, for as long as it stays open. Its error
// says why it ended, which nobody is waiting on: the next node is asked.
func (s *server) session(ctx context.Context, id libp2ppeer.ID) error {
	c, err := rpc.NewClient(s.self, id, build.Protocol)
	if err != nil {
		return err
	}
	defer c.Close()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream := buildv2connect.NewRunnerServiceClient(c.HTTP(), c.URL(), codec.Option()).Serve(ctx)
	defer stream.CloseRequest()
	// A CANCELLED CONTEXT DOES NOT END A READ IN PROGRESS over this transport, so the
	// stream's response is closed under it, which does.
	defer context.AfterFunc(ctx, func() { stream.CloseResponse() })()

	send := &sender{stream: stream}
	if err := send.report(&buildv2.RunnerReport{Kind: &buildv2.RunnerReport_Ready{Ready: &buildv2.Empty{}}}); err != nil {
		return err
	}

	var job *running
	defer func() {
		if job != nil {
			job.cancel()
			<-job.done
		}
		// WHAT THE JOB SAID LAST IS STILL ON ITS WAY, and a cancel would cut it: the
		// request is closed and the build node's end of the stream awaited.
		stream.CloseRequest()
		for {
			if _, err := stream.Receive(); err != nil {
				return
			}
		}
	}()
	for {
		a, err := stream.Receive()
		if err != nil {
			return err
		}
		if job != nil {
			select {
			case <-job.done:
				job = nil
			default:
			}
		}
		switch k := a.GetKind().(type) {
		case *buildv2.Assignment_Cancel:
			if job != nil {
				job.cancel()
			}
		case *buildv2.Assignment_Plan, *buildv2.Assignment_Build:
			if job != nil {
				return errors.New("runner: handed a job while one is in hand")
			}
			job = s.start(ctx, a, send)
		default:
			return fmt.Errorf("runner: an assignment this runner does not know: %T", k)
		}
	}
}

// running is the job in hand.
type running struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// start runs one job beside the stream's reader, which goes on reading so a cancel
// reaches it; the job is the stream's only sender until it has said how it ended.
func (s *server) start(ctx context.Context, a *buildv2.Assignment, send *sender) *running {
	jobCtx, cancel := context.WithCancel(ctx)
	r := &running{cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(r.done)
		defer cancel()
		var (
			environments []string
			err          error
		)
		if w := a.GetPlan(); w != nil {
			environments, err = s.plan(jobCtx, workFrom(w), send)
		} else {
			w := workFrom(a.GetBuild())
			err = s.exec.Run(jobCtx, w, &output{sender: send, assets: s.assets, work: w})
		}
		finished := &buildv2.Finished{Environments: environments}
		if err != nil {
			finished.Error = err.Error()
		}
		send.report(&buildv2.RunnerReport{Kind: &buildv2.RunnerReport_Finished{Finished: finished}})
	}()
	return r
}

// plan is a push read: confirmed at the forge first, so a push is built only for
// what the repository holds, then which environments it feeds.
func (s *server) plan(ctx context.Context, w run.Work, out io.Writer) ([]string, error) {
	if s.confirm != nil {
		if err := s.confirm.Confirm(ctx, w.RepositoryKey, w.Ref, w.CommitSHA); err != nil {
			return nil, err
		}
	}
	return s.exec.Plan(ctx, w, out)
}

func workFrom(w *buildv2.Work) run.Work {
	return run.Work{
		JobID:         w.GetJobId(),
		RepositoryKey: w.GetRepositoryKey(),
		Ref:           w.GetRef(),
		CommitSHA:     w.GetCommitSha(),
		Before:        w.GetBefore(),
		ProjectID:     w.GetProjectId(),
		Attempt:       w.GetAttempt(),
		Environment:   w.GetEnvironment(),
	}
}

// sender is the stream's sending half, which one job writes from as many
// goroutines as its build does.
type sender struct {
	mu     sync.Mutex
	stream *connect.BidiStreamForClient[buildv2.RunnerReport, buildv2.Assignment]
}

func (s *sender) report(r *buildv2.RunnerReport) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stream.Send(r)
}

// Write is a plan's output: what it said, sent as it said it.
func (s *sender) Write(p []byte) (int, error) {
	if err := s.report(&buildv2.RunnerReport{Kind: &buildv2.RunnerReport_Output{Output: append([]byte(nil), p...)}}); err != nil {
		return 0, err
	}
	return len(p), nil
}

// output is a build's run.Output: its log sent up the stream, its artifacts kept in
// the storage service by this runner and named up the stream, so their bytes never
// cross build.
type output struct {
	*sender
	assets Assets
	work   run.Work
}

func (o *output) Asset(ctx context.Context, resourceID string, content io.Reader) error {
	if o.work.Environment == "" {
		return errors.New("runner: a plan builds nothing, so it has no artifact to keep")
	}
	if o.assets == nil {
		return errors.New("runner: no storage service to keep an artifact in")
	}
	at, err := storage.NewAddress(o.work.ProjectID, o.work.Environment, storage.AssetsBucket, resourceID)
	if err != nil {
		return fmt.Errorf("runner: job %s: keeping %s: %w", o.work.JobID, resourceID, err)
	}
	counted := &counting{from: content}
	v, err := o.assets.Put(ctx, at, o.work.JobID, storage.Object{}, counted)
	if err != nil {
		return fmt.Errorf("runner: job %s: keeping %s: %w", o.work.JobID, resourceID, err)
	}
	if v.Record.RootCID == "" {
		return fmt.Errorf("runner: job %s: the storage service named no cid for %s", o.work.JobID, resourceID)
	}
	return o.report(&buildv2.RunnerReport{Kind: &buildv2.RunnerReport_Stored{Stored: &buildv2.Stored{
		ResourceId: resourceID, Cid: v.Record.RootCID, Bytes: uint64(counted.n),
	}}})
}

// counting counts what it hands over: an artifact's size is what a plan bills, and a
// cid says nothing about it.
type counting struct {
	from io.Reader
	n    int64
}

func (c *counting) Read(p []byte) (int, error) {
	n, err := c.from.Read(p)
	c.n += int64(n)
	return n, err
}

// Close stops offering this runner, abandoning the job in hand: its claim lapses
// and build hands it to another runner.
func (s *server) Close() error {
	if s.stop != nil {
		s.stop()
		<-s.stopped
	}
	var errs []error
	for i := len(s.also) - 1; i >= 0; i-- {
		errs = append(errs, s.also[i].Close())
	}
	s.also = nil
	return errors.Join(errs...)
}
