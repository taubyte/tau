// Package builder is the private service that builds, N of them: it holds no state and
// runs what plm hands it. A builder serves its own protocol (builder.proto) on
// /tau/builder/v2 over p2p and faces nothing else; plm finds builders by that
// protocol through p2p discovery and hands each one run at a time. A run's log lives here until plm
// seals it, and plm proxies a follower to it (.notes/v2/build-topology.md).
package builder

import (
	"context"
	"errors"
	"io"
	"net/http"

	"connectrpc.com/connect"

	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/proto/codec"
	builderv2 "github.com/taubyte/tau/pkg/proto/gen/tau/builder/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/builder/v2/builderv2connect"
	plmv2 "github.com/taubyte/tau/pkg/proto/gen/tau/plm/v2"
	"github.com/taubyte/tau/services/plm"
	"github.com/taubyte/tau/services/plm/run"
	"github.com/taubyte/tau/services/storage"
)

// Assets is where a builder keeps what a job built, and its logs: the storage service.
type Assets interface {
	Put(ctx context.Context, at storage.Address, name string, o storage.Object, content io.Reader) (storage.Version, error)
}

// New is the builder service, unstarted. Start builds it from its instance.
func New() service.Service { return &server{} }

// server is the builder service: its runs, served on BuilderProtocol, and reached as
// a run.Builder by a plm beside it on the same instance, which does not dial itself.
type server struct {
	*Runs
	listen *rpc.Server
}

var _ run.Builder = (*server)(nil)

// Start runs the builder on an instance, and serves its runs on BuilderProtocol.
func (s *server) Start(ctx context.Context, h service.Host) error {
	if s.Runs != nil {
		return errors.New("builder: already started")
	}
	var cfg Config
	if err := h.Config(&cfg); err != nil {
		return err
	}
	runs, err := Open(h, cfg)
	if err != nil {
		return err
	}
	listen, err := rpc.Listen(ctx, h.Peer().Host(), plm.BuilderProtocol, Handler(runs))
	if err != nil {
		return errors.Join(err, runs.Close())
	}
	s.Runs, s.listen = runs, listen
	return nil
}

// Close stops serving, abandoning the run in hand: its claim lapses and plm hands
// it to another builder.
func (s *server) Close() error {
	var errs []error
	if s.listen != nil {
		errs = append(errs, s.listen.Close())
	}
	if s.Runs != nil {
		errs = append(errs, s.Runs.Close())
	}
	return errors.Join(errs...)
}

// maxRequest bounds one request a builder reads.
const maxRequest = 1 << 20

// Handler is runs served as builder.proto.
func Handler(runs run.Builder) http.Handler {
	mux := http.NewServeMux()
	mux.Handle(builderv2connect.NewBuilderServiceHandler(handler{runs}, connect.WithReadMaxBytes(maxRequest), codec.Option()))
	return mux
}

type handler struct{ runs run.Builder }

var _ builderv2connect.BuilderServiceHandler = handler{}

func (h handler) Plan(ctx context.Context, req *connect.Request[builderv2.Work]) (*connect.Response[builderv2.Planned], error) {
	out, err := h.runs.Plan(ctx, run.WorkOf(req.Msg))
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(out), nil
}

func (h handler) Build(ctx context.Context, req *connect.Request[builderv2.Work]) (*connect.Response[builderv2.Built], error) {
	out, err := h.runs.Build(ctx, run.WorkOf(req.Msg))
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(out), nil
}

func (h handler) Cancel(ctx context.Context, req *connect.Request[builderv2.Run]) (*connect.Response[plmv2.Empty], error) {
	if err := h.runs.Cancel(ctx, req.Msg); err != nil {
		return nil, err
	}
	return connect.NewResponse(&plmv2.Empty{}), nil
}

func (h handler) Seal(ctx context.Context, req *connect.Request[builderv2.Sealing]) (*connect.Response[builderv2.Sealed], error) {
	out, err := h.runs.Seal(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(out), nil
}

func (h handler) Follow(ctx context.Context, req *connect.Request[plmv2.FollowRequest], stream *connect.ServerStream[plmv2.Job_Event]) error {
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
