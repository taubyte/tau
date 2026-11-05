package clients

import (
	"context"
	"io"
	"sync"

	"connectrpc.com/connect"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/proto/codec"
	buildv2 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v2"
	runnerv2 "github.com/taubyte/tau/pkg/proto/gen/tau/runner/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/runner/v2/runnerv2connect"
	"github.com/taubyte/tau/services/build"
	"github.com/taubyte/tau/services/build/run"
)

// Runner is one runner, reached over its protocol: what build hands a run to.
type Runner struct {
	conn *rpc.Client
	rpc  runnerv2connect.RunnerServiceClient
}

// RunnerAt is the runner id is, dialled from h.
func RunnerAt(h host.Host, id peer.ID) (*Runner, error) {
	c, err := rpc.NewClient(h, id, build.RunnerProtocol)
	if err != nil {
		return nil, err
	}
	return &Runner{conn: c, rpc: runnerv2connect.NewRunnerServiceClient(c.HTTP(), c.URL(), codec.Option())}, nil
}

func (r *Runner) Plan(ctx context.Context, w run.Work) (*runnerv2.Planned, error) {
	res, err := r.rpc.Plan(ctx, connect.NewRequest(w.Proto()))
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

func (r *Runner) Build(ctx context.Context, w run.Work) (*runnerv2.Built, error) {
	res, err := r.rpc.Build(ctx, connect.NewRequest(w.Proto()))
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

func (r *Runner) Cancel(ctx context.Context, which *runnerv2.Run) error {
	_, err := r.rpc.Cancel(ctx, connect.NewRequest(which))
	return err
}

func (r *Runner) Seal(ctx context.Context, s *runnerv2.Sealing) (*runnerv2.Sealed, error) {
	res, err := r.rpc.Seal(ctx, connect.NewRequest(s))
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

// Follow is the run's log as the runner streams it. The stream is opened before it
// returns, so a runner that holds no such log is its error here.
func (r *Runner) Follow(ctx context.Context, req *buildv2.FollowRequest) (run.Events, error) {
	ctx, cancel := context.WithCancel(ctx)
	stream, err := r.rpc.Follow(ctx, connect.NewRequest(req))
	if err != nil {
		cancel()
		return nil, err
	}
	f := &followed{stream: stream, cancel: cancel}
	if !stream.Receive() {
		defer f.Close()
		if err := stream.Err(); err != nil {
			return nil, err
		}
		f.done = true
		return f, nil
	}
	f.first = stream.Msg()
	return f, nil
}

// Close lets go of the runner's connections.
func (r *Runner) Close() error {
	r.conn.Close()
	return nil
}

// followed is a runner's Follow stream, its first event read ahead.
type followed struct {
	stream *connect.ServerStreamForClient[buildv2.Job_Event]
	cancel context.CancelFunc
	first  *buildv2.Job_Event
	done   bool
}

func (f *followed) Receive() (*buildv2.Job_Event, error) {
	if ev := f.first; ev != nil {
		f.first = nil
		return ev, nil
	}
	if f.done || !f.stream.Receive() {
		f.done = true
		if err := f.stream.Err(); err != nil {
			return nil, err
		}
		return nil, io.EOF
	}
	return f.stream.Msg(), nil
}

func (f *followed) Close() error {
	f.cancel()
	return f.stream.Close()
}

// Runners is the runners discovery names beside a build node, each dialled once and
// kept until Close.
type Runners struct {
	h       service.Host
	mu      sync.Mutex
	dialled map[peer.ID]*Runner
}

// RunnersOn is the runners the directory beside h names.
func RunnersOn(h service.Host) *Runners {
	return &Runners{h: h, dialled: map[peer.ID]*Runner{}}
}

// Peers is every runner the directory names now: none on an instance running no
// directory.
func (r *Runners) Peers() []peer.ID {
	return Discovered(r.h, build.Runner.Name).Known()
}

// At is the runner id.
func (r *Runners) At(id peer.ID) (run.Service, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if c, ok := r.dialled[id]; ok {
		return c, nil
	}
	c, err := RunnerAt(r.h.Peer().Host(), id)
	if err != nil {
		return nil, err
	}
	r.dialled[id] = c
	return c, nil
}

// Close lets go of every runner dialled.
func (r *Runners) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, c := range r.dialled {
		c.Close()
		delete(r.dialled, id)
	}
	return nil
}
