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
// signatures, so a Client on a certificate node calls it without a stream.
type handler struct{ s *Service }

var (
	_ globalsv2connect.CertificateServiceHandler = handler{}
	_ globalsv2connect.CertificateServiceClient  = handler{}
)

func (h handler) Certificate(ctx context.Context, req *connect.Request[globalsv2.CertificateRequest]) (*connect.Response[globalsv2.Issued], error) {
	name := normal(req.Msg.GetName())
	var k *kept
	var err error
	if req.Msg.GetIssue() {
		k, err = h.s.Ensure(ctx, name)
	} else {
		k, err = h.s.newest(ctx, h.s.served(name))
	}
	if k == nil {
		return nil, wire(err)
	}
	return connect.NewResponse(k.record), nil
}

func (h handler) KeyAuthorization(ctx context.Context, req *connect.Request[globalsv2.KeyAuthorizationRequest]) (*connect.Response[globalsv2.KeyAuthorizationValue], error) {
	value, found, err := h.s.KeyAuthorization(ctx, req.Msg.GetToken())
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, err)
	}
	return connect.NewResponse(&globalsv2.KeyAuthorizationValue{Found: found, Value: value}), nil
}

func (h handler) Challenges(ctx context.Context, req *connect.Request[globalsv2.ChallengesRequest]) (*connect.Response[globalsv2.ChallengeValues], error) {
	values, err := h.s.Challenges(ctx, req.Msg.GetFqdn())
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, err)
	}
	return connect.NewResponse(&globalsv2.ChallengeValues{Values: values}), nil
}

func wire(err error) error {
	switch {
	case errors.Is(err, ErrNotZone):
		return connect.NewError(connect.CodeInvalidArgument, err)
	case errors.Is(err, ErrNotKept):
		return connect.NewError(connect.CodeNotFound, err)
	}
	return connect.NewError(connect.CodeUnavailable, err)
}
