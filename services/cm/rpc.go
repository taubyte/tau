package cm

import (
	"context"
	"errors"
	"net/http"

	"connectrpc.com/connect"

	cmv2 "github.com/taubyte/tau/pkg/proto/gen/tau/cm/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/cm/v2/cmv2connect"
)

func (s *Service) mount() http.Handler {
	mux := http.NewServeMux()
	mux.Handle(cmv2connect.NewCertificateServiceHandler(handler{s}))
	mux.Handle(cmv2connect.NewDomainServiceHandler(handler{s}))
	return mux
}

// handler is the generated surface. Its methods have the generated client's
// signatures, so a Client on a cm node calls it without a stream.
type handler struct{ s *Service }

var (
	_ cmv2connect.CertificateServiceHandler = handler{}
	_ cmv2connect.CertificateServiceClient  = handler{}
	_ cmv2connect.DomainServiceHandler      = handler{}
	_ cmv2connect.DomainServiceClient       = handler{}
)

func (h handler) Certificate(ctx context.Context, req *connect.Request[cmv2.CertificateRequest]) (*connect.Response[cmv2.Issued], error) {
	k, err := h.s.newest(ctx, normal(req.Msg.GetName()))
	if err != nil {
		return nil, wire(err)
	}
	return connect.NewResponse(k.record), nil
}

func (h handler) Keep(ctx context.Context, req *connect.Request[cmv2.Issued]) (*connect.Response[cmv2.Empty], error) {
	if err := h.s.keep(ctx, req.Msg); err != nil {
		return nil, wire(err)
	}
	return connect.NewResponse(&cmv2.Empty{}), nil
}

func wire(err error) error {
	switch {
	case errors.Is(err, ErrNotKept):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, ErrNotZone), errors.Is(err, ErrBadClaim), errors.Is(err, ErrUnproven):
		return connect.NewError(connect.CodeInvalidArgument, err)
	}
	return connect.NewError(connect.CodeUnavailable, err)
}
