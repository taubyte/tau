package jobs

import (
	"context"
	"errors"
	"io"
	"sync"

	"connectrpc.com/connect"

	buildv2 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v2"
	"github.com/taubyte/tau/pkg/queue"
	"github.com/taubyte/tau/services/build/run"
)

// The runners a build node hands jobs to. A runner opens RunnerService.Serve to the
// node leading the queue and says it is ready; the claim loop takes a free one,
// claims a job for it, and runs the job there, the stream carrying the job down and
// what it produced back up. The handler's goroutine is the stream's reader; build
// never dials a runner.

// errRunnerLost is a run whose runner went away. It is not the build's failure: the
// run is abandoned, its claim lapses, and the queue hands the job to another runner.
var errRunnerLost = errors.New("build: the runner running it went away")

// runners is the streams this node holds that take no job right now.
type runners struct {
	mu   sync.Mutex
	free []*stream
	// wake rings when there is something to hand out or someone to hand it to: a
	// job queued, a runner joining or finishing one. The claim loop dispatches on it
	// rather than on its next tick. A stream put back because there was nothing to
	// claim does not ring it, or the loop would spin.
	wake notice
}

// take is a free stream, no longer free.
func (r *runners) take() (*stream, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for len(r.free) > 0 {
		st := r.free[len(r.free)-1]
		r.free = r.free[:len(r.free)-1]
		if !st.ended() {
			return st, true
		}
	}
	return nil, false
}

// give is st free again, unless it ended, and the loop told.
func (r *runners) give(st *stream) {
	if r.putBack(st) {
		r.wake.ring()
	}
}

// putBack is st free again, unless it ended, and the loop not told.
func (r *runners) putBack(st *stream) bool {
	if st.ended() {
		return false
	}
	r.mu.Lock()
	r.free = append(r.free, st)
	r.mu.Unlock()
	return true
}

// stream is one runner's: what is sent down it, and where what it says goes while
// it runs a job.
type stream struct {
	bidi func(*buildv2.Assignment) error
	gone chan struct{}
	once sync.Once

	// sendMu orders sends, and shut is set once the handler is returning: a send
	// after that would write to a stream connect has already closed.
	sendMu sync.Mutex
	shut   bool

	mu     sync.Mutex
	active chan *buildv2.RunnerReport
}

// send is a to the runner, or errRunnerLost once its handler is returning.
func (st *stream) send(a *buildv2.Assignment) error {
	st.sendMu.Lock()
	defer st.sendMu.Unlock()
	if st.shut {
		return errRunnerLost
	}
	return st.bidi(a)
}

// shutDown is the last word on sends, said as the handler returns; one in progress
// finishes first.
func (st *stream) shutDown() {
	st.sendMu.Lock()
	st.shut = true
	st.sendMu.Unlock()
}

// end is the stream gone, said once: when its handler returns, or when the runner's
// side of it is cancelled, which a read in progress does not always see.
func (st *stream) end() { st.once.Do(func() { close(st.gone) }) }

func (st *stream) ended() bool {
	select {
	case <-st.gone:
		return true
	default:
		return false
	}
}

// hand is where the job in hand hears what the runner says. A report while no job
// is in hand is the end of one that was let go, and is dropped.
func (st *stream) hand(rep *buildv2.RunnerReport) {
	st.mu.Lock()
	active := st.active
	st.mu.Unlock()
	if active == nil {
		return
	}
	select {
	case active <- rep:
	case <-st.gone:
	}
}

// serveRunner is one runner's stream, for as long as it stays open: only on the
// leader, where every job is claimed, so a runner on a follower walks on.
func (s *server) serveRunner(ctx context.Context, bidi *connect.BidiStream[buildv2.RunnerReport, buildv2.Assignment]) error {
	if err := s.allow(ctx, ActServe, ""); err != nil {
		return err
	}
	if !s.store.IsLeader() {
		return errUnavailable("not the leader of the build queue; a runner serves the one that is")
	}
	first, err := bidi.Receive()
	if err != nil {
		return err
	}
	if first.GetReady() == nil {
		return errInvalidArgument("a runner's first word is that it is ready")
	}
	st := &stream{bidi: bidi.Send, gone: make(chan struct{})}
	defer st.end()
	defer st.shutDown()
	defer context.AfterFunc(ctx, st.end)()
	s.runners.give(st)
	for {
		rep, err := bidi.Receive()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		st.hand(rep)
	}
}

// remote is a run.Runner that is one runner's stream.
type remote struct{ st *stream }

var _ run.Runner = remote{}

func (r remote) Run(ctx context.Context, w run.Work, out run.Output) error {
	_, err := r.do(ctx, &buildv2.Assignment{Kind: &buildv2.Assignment_Build{Build: workOf(w)}}, out)
	return err
}

func (r remote) Plan(ctx context.Context, w run.Work, out io.Writer) ([]string, error) {
	return r.do(ctx, &buildv2.Assignment{Kind: &buildv2.Assignment_Plan{Plan: workOf(w)}}, out)
}

// do hands the runner one job and reads what it says until the job ends or the
// runner goes. A job let go (cancelled, or its claim ran out) is told to stop, and
// what it says after is still read until it ends, so the stream is free again only
// once the runner is.
func (r remote) do(ctx context.Context, a *buildv2.Assignment, out io.Writer) ([]string, error) {
	reports := make(chan *buildv2.RunnerReport)
	r.st.mu.Lock()
	r.st.active = reports
	r.st.mu.Unlock()
	defer func() {
		r.st.mu.Lock()
		r.st.active = nil
		r.st.mu.Unlock()
	}()

	if err := r.st.send(a); err != nil {
		return nil, errRunnerLost
	}
	done := ctx.Done()
	for {
		select {
		case rep := <-reports:
			switch k := rep.GetKind().(type) {
			case *buildv2.RunnerReport_Output:
				// A log that will not take more is the run's to report at its end; the
				// stream is still read so the runner is not held up.
				out.Write(k.Output)
			case *buildv2.RunnerReport_Stored:
				if kept, ok := out.(storedAsset); ok {
					if err := kept.stored(ctx, k.Stored.GetResourceId(), k.Stored.GetCid(), k.Stored.GetBytes()); err != nil {
						return nil, err
					}
				}
			case *buildv2.RunnerReport_Finished:
				if done == nil {
					return nil, context.Cause(ctx)
				}
				if msg := k.Finished.GetError(); msg != "" {
					return nil, errors.New(msg)
				}
				return k.Finished.GetEnvironments(), nil
			}
		case <-r.st.gone:
			return nil, errRunnerLost
		case <-done:
			if err := r.st.send(&buildv2.Assignment{Kind: &buildv2.Assignment_Cancel{Cancel: &buildv2.Empty{}}}); err != nil {
				return nil, errRunnerLost
			}
			done = nil
		}
	}
}

// storedAsset is an output that records an artifact the runner already kept.
type storedAsset interface {
	stored(ctx context.Context, resourceID, cid string, bytes uint64) error
}

func workOf(w run.Work) *buildv2.Work {
	return &buildv2.Work{
		JobId:         w.JobID,
		RepositoryKey: w.RepositoryKey,
		Ref:           w.Ref,
		CommitSha:     w.CommitSHA,
		Before:        w.Before,
		ProjectId:     w.ProjectID,
		Attempt:       w.Attempt,
		Environment:   w.Environment,
	}
}

// dispatch claims a job for every free runner and runs each on its own: a run per
// runner, every write still a section under jobLock, as an rpc goroutine's is.
func (s *server) dispatch(ctx context.Context) error {
	for {
		st, ok := s.runners.take()
		if !ok {
			return nil
		}
		d, err := s.work.Claim()
		if err != nil {
			s.runners.putBack(st)
			if errors.Is(err, queue.ErrNoItem) {
				return nil
			}
			return err
		}
		s.dispatched.Add(1)
		go s.runOn(ctx, d, st)
	}
}

// runOn is one dispatched job, its runner free again once the job has ended.
// Its error is the loop's kind, with nobody to return it to: the next cycle and
// the claim's lapse are the retry.
func (s *server) runOn(ctx context.Context, d queue.Delivery, st *stream) {
	defer s.dispatched.Done()
	s.run(ctx, d, remote{st})
	s.runners.give(st)
}
