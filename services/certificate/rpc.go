package certificate

import (
	"context"
	"errors"
	"net/http"

	"connectrpc.com/connect"

	certificatev2 "github.com/taubyte/tau/pkg/proto/gen/tau/certificate/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/certificate/v2/certificatev2connect"
)

func (s *Service) mount() http.Handler {
	mux := http.NewServeMux()
	mux.Handle(certificatev2connect.NewCertificateServiceHandler(handler{s}))
	return mux
}

// handler is the generated surface. Its methods have the generated client's
// signatures, so a Client on a certificate node calls it without a stream.
type handler struct{ s *Service }

var (
	_ certificatev2connect.CertificateServiceHandler = handler{}
	_ certificatev2connect.CertificateServiceClient  = handler{}
)

func (h handler) Certificate(ctx context.Context, req *connect.Request[certificatev2.CertificateRequest]) (*connect.Response[certificatev2.Issued], error) {
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

func (h handler) Thumbprint(ctx context.Context, _ *connect.Request[certificatev2.Empty]) (*connect.Response[certificatev2.AccountThumbprint], error) {
	value, err := h.s.Thumbprint(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, err)
	}
	return connect.NewResponse(&certificatev2.AccountThumbprint{Value: value}), nil
}

func (h handler) Challenges(ctx context.Context, req *connect.Request[certificatev2.ChallengesRequest]) (*connect.Response[certificatev2.ChallengeValues], error) {
	values, err := h.s.Challenges(ctx, req.Msg.GetFqdn())
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, err)
	}
	return connect.NewResponse(&certificatev2.ChallengeValues{Values: values}), nil
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
