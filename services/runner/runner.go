// Package runner is the private build service, N of them: it holds no state and
// runs what build hands it. A runner serves its own protocol (runner.proto) on
// /tau/runner/v2 over p2p and faces nothing else; build finds runners through
// discovery and hands each one run at a time. A run's log lives here until build
// seals it, and build proxies a follower to it (.notes/v2/build-topology.md).
package runner

import (
	"context"
	"errors"
	"io"
	"net/http"

	"connectrpc.com/connect"

	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/proto/codec"
	buildv2 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v2"
	runnerv2 "github.com/taubyte/tau/pkg/proto/gen/tau/runner/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/runner/v2/runnerv2connect"
	"github.com/taubyte/tau/services/build"
	"github.com/taubyte/tau/services/build/run"
	"github.com/taubyte/tau/services/storage"
)

// Assets is where a runner keeps what a job built, and its logs: the storage service.
type Assets interface {
	Put(ctx context.Context, at storage.Address, name string, o storage.Object, content io.Reader) (storage.Version, error)
}

// New is the runner service, unstarted. Start builds it from its instance.
func New() service.Service { return &server{} }

type server struct {
	runs   *Runs
	listen *rpc.Server
}

// Start runs the runner on an instance, and serves its runs on RunnerProtocol.
func (s *server) Start(ctx context.Context, h service.Host) error {
	if s.runs != nil {
		return errors.New("runner: already started")
	}
	var cfg Config
	if err := h.Config(&cfg); err != nil {
		return err
	}
	runs, err := Open(h, cfg)
	if err != nil {
		return err
	}
	listen, err := rpc.Listen(ctx, h.Peer().Host(), build.RunnerProtocol, Handler(runs))
	if err != nil {
		return errors.Join(err, runs.Close())
	}
	s.runs, s.listen = runs, listen
	return nil
}

// Close stops serving, abandoning the run in hand: its claim lapses and build hands
// it to another runner.
func (s *server) Close() error {
	var errs []error
	if s.listen != nil {
		errs = append(errs, s.listen.Close())
	}
	if s.runs != nil {
		errs = append(errs, s.runs.Close())
	}
	return errors.Join(errs...)
}

// maxRequest bounds one request a runner reads.
const maxRequest = 1 << 20

// Handler is runs served as runner.proto.
func Handler(runs run.Service) http.Handler {
	mux := http.NewServeMux()
	mux.Handle(runnerv2connect.NewRunnerServiceHandler(handler{runs}, connect.WithReadMaxBytes(maxRequest), codec.Option()))
	return mux
}

type handler struct{ runs run.Service }

var _ runnerv2connect.RunnerServiceHandler = handler{}

func (h handler) Plan(ctx context.Context, req *connect.Request[runnerv2.Work]) (*connect.Response[runnerv2.Planned], error) {
	out, err := h.runs.Plan(ctx, run.WorkOf(req.Msg))
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(out), nil
}

func (h handler) Build(ctx context.Context, req *connect.Request[runnerv2.Work]) (*connect.Response[runnerv2.Built], error) {
	out, err := h.runs.Build(ctx, run.WorkOf(req.Msg))
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(out), nil
}

func (h handler) Cancel(ctx context.Context, req *connect.Request[runnerv2.Run]) (*connect.Response[buildv2.Empty], error) {
	if err := h.runs.Cancel(ctx, req.Msg); err != nil {
		return nil, err
	}
	return connect.NewResponse(&buildv2.Empty{}), nil
}

func (h handler) Seal(ctx context.Context, req *connect.Request[runnerv2.Sealing]) (*connect.Response[runnerv2.Sealed], error) {
	out, err := h.runs.Seal(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(out), nil
}

func (h handler) Follow(ctx context.Context, req *connect.Request[buildv2.FollowRequest], stream *connect.ServerStream[buildv2.Job_Event]) error {
	events, err := h.runs.Follow(ctx, req.Msg)
	if err != nil {
		return err
	}
	defer events.Close()
	for {
		ev, err := events.Receive()
		if errors.Is(err, io.EOF) || ctx.Err() != nil {
			return nil
		}
		if err != nil {
			return connect.NewError(connect.CodeUnavailable, err)
		}
		if err := stream.Send(ev); err != nil {
			return err
		}
	}
}
