package dream

import (
	"context"
	"errors"
	"net"
	"net/http"

	"connectrpc.com/connect"

	"github.com/taubyte/tau/pkg/proto/codec"
	dreamv2 "github.com/taubyte/tau/pkg/proto/gen/tau/dream/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/dream/v2/dreamv2connect"
)

// rpc.go is DreamService (pkg/proto/tau/dream/v2): the universe's Go methods, for a
// tool outside its process.

// served is the DreamService a universe answers on, closed with it.
type served struct {
	listener net.Listener
	server   *http.Server
}

// serve answers DreamService for u on addr.
func (u *Universe) serve(addr string) error {
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.Handle(dreamv2connect.NewDreamServiceHandler(handler{u}, codec.Option()))
	s := &served{listener: l, server: &http.Server{Handler: mux}}
	go s.server.Serve(l)
	u.rpc = s
	return nil
}

func (s *served) Close() error {
	if err := s.server.Close(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Addr is where this universe answers DreamService, nil when its Config named no
// Listen.
func (u *Universe) Addr() net.Addr {
	if u.rpc == nil {
		return nil
	}
	return u.rpc.listener.Addr()
}

type handler struct{ u *Universe }

var _ dreamv2connect.DreamServiceHandler = handler{}

func (h handler) Commit(_ context.Context, req *connect.Request[dreamv2.CommitRequest]) (*connect.Response[dreamv2.Push], error) {
	m := req.Msg
	commit, err := h.u.Commit(m.GetRepository(), m.GetBranch(), m.GetDir())
	if err != nil {
		return nil, refused(err)
	}
	return connect.NewResponse(&dreamv2.Push{Repository: m.GetRepository(), Ref: "refs/heads/" + m.GetBranch(), Commit: commit}), nil
}

func (h handler) Upload(_ context.Context, req *connect.Request[dreamv2.UploadRequest]) (*connect.Response[dreamv2.Push], error) {
	m := req.Msg
	p, err := h.u.Upload(m.GetRepository(), m.GetBranch(), m.GetDir(), m.GetBuilt())
	if err != nil {
		return nil, refused(err)
	}
	return connect.NewResponse(&dreamv2.Push{Repository: p.Repository, Ref: p.Ref, Commit: p.Commit}), nil
}

func (h handler) Link(ctx context.Context, req *connect.Request[dreamv2.LinkRequest]) (*connect.Response[dreamv2.Linked], error) {
	m := req.Msg
	if err := h.u.Link(ctx, m.GetProject(), m.GetConfig(), m.GetCode()...); err != nil {
		return nil, refused(err)
	}
	return connect.NewResponse(&dreamv2.Linked{}), nil
}

func (h handler) Build(ctx context.Context, req *connect.Request[dreamv2.Push]) (*connect.Response[dreamv2.Outcome], error) {
	m := req.Msg
	out, err := h.u.Build(ctx, Push{Repository: m.GetRepository(), Ref: m.GetRef(), Commit: m.GetCommit()})
	if err != nil {
		return nil, refused(err)
	}
	return connect.NewResponse(&dreamv2.Outcome{Plan: out.Plan, Environments: out.Environments}), nil
}

// refused keeps a code the universe's services said, and is a request the universe
// could not do anything with otherwise.
func refused(err error) error {
	var coded *connect.Error
	if errors.As(err, &coded) {
		return err
	}
	return connect.NewError(connect.CodeFailedPrecondition, err)
}
