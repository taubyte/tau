package globals

import (
	"context"
	"errors"
	"net/http"

	"connectrpc.com/connect"

	globalsv2 "github.com/taubyte/tau/pkg/proto/gen/tau/globals/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/globals/v2/globalsv2connect"
)

func (s *Service) mount() http.Handler {
	mux := http.NewServeMux()
	mux.Handle(globalsv2connect.NewCertificateServiceHandler(handler{s}))
	return mux
}

// handler is the generated surface. Its methods have the generated client's
// signatures, so a Client on a globals node calls it without a stream.
type handler struct{ s *Service }

var (
	_ globalsv2connect.CertificateServiceHandler = handler{}
	_ globalsv2connect.CertificateServiceClient  = handler{}
)

func (h handler) Certificate(ctx context.Context, req *connect.Request[globalsv2.CertificateRequest]) (*connect.Response[globalsv2.Issued], error) {
	k, err := h.s.newest(ctx, normal(req.Msg.GetName()))
	if err != nil {
		return nil, wire(err)
	}
	return connect.NewResponse(k.record), nil
}

func (h handler) Keep(ctx context.Context, req *connect.Request[globalsv2.Issued]) (*connect.Response[globalsv2.Empty], error) {
	if err := h.s.keep(ctx, req.Msg); err != nil {
		return nil, wire(err)
	}
	return connect.NewResponse(&globalsv2.Empty{}), nil
}

func wire(err error) error {
	switch {
	case errors.Is(err, ErrNotKept):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, ErrNotZone):
		return connect.NewError(connect.CodeInvalidArgument, err)
	}
	return connect.NewError(connect.CodeUnavailable, err)
}
