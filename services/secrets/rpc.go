package secrets

import (
	"context"
	"errors"
	"net/http"

	"connectrpc.com/connect"

	secretsv2 "github.com/taubyte/tau/pkg/proto/gen/tau/secrets/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/secrets/v2/secretsv2connect"
)

// rpc.go is the whole wire surface: four calls, each one line of work and one
// line of error mapping.
//
// NO HANDLER VALIDATES ANYTHING. The key grammar is vault.go's, checked there
// once. A check repeated here would be a second opinion, and the day the two
// disagreed the answer would depend on which door the request came through.

// mount is the one handler rpc.Listen serves.
func (s *Service) mount() http.Handler {
	mux := http.NewServeMux()
	mux.Handle(secretsv2connect.NewSecretsServiceHandler(&handler{s}))
	return mux
}

type handler struct{ s *Service }

var _ secretsv2connect.SecretsServiceHandler = (*handler)(nil)

func (h *handler) Get(ctx context.Context, req *connect.Request[secretsv2.SecretRequest]) (*connect.Response[secretsv2.SecretValue], error) {
	scope := req.Msg.GetScope()
	value, err := h.s.vault.Get(ctx, scope.GetProject(), scope.GetEnvironment(), req.Msg.GetName())
	if err != nil {
		return nil, wire(err)
	}
	return connect.NewResponse(&secretsv2.SecretValue{Value: value}), nil
}

// Set is an upsert: rotating is setting again.
//
// AUTHORIZATION IS NOT HERE, and the omission is stated rather than implied:
// any peer that can reach this service can write any project's secrets, and
// read them back. See the package doc and .notes/tee/v2-seam.md.
func (h *handler) Set(ctx context.Context, req *connect.Request[secretsv2.SetRequest]) (*connect.Response[secretsv2.SetResponse], error) {
	scope := req.Msg.GetScope()
	if err := h.s.vault.Set(ctx, scope.GetProject(), scope.GetEnvironment(), req.Msg.GetName(), req.Msg.GetValue()); err != nil {
		return nil, wire(err)
	}
	return connect.NewResponse(&secretsv2.SetResponse{}), nil
}

func (h *handler) Delete(ctx context.Context, req *connect.Request[secretsv2.SecretRequest]) (*connect.Response[secretsv2.DeleteResponse], error) {
	scope := req.Msg.GetScope()
	if err := h.s.vault.Delete(ctx, scope.GetProject(), scope.GetEnvironment(), req.Msg.GetName()); err != nil {
		return nil, wire(err)
	}
	return connect.NewResponse(&secretsv2.DeleteResponse{}), nil
}

func (h *handler) List(ctx context.Context, req *connect.Request[secretsv2.Scope]) (*connect.Response[secretsv2.SecretNames], error) {
	names, err := h.s.vault.List(ctx, req.Msg.GetProject(), req.Msg.GetEnvironment())
	if err != nil {
		return nil, wire(err)
	}
	return connect.NewResponse(&secretsv2.SecretNames{Names: names}), nil
}

// wire is the one code table: every connect.NewError in this package goes
// through it, so the mapping from a vault failure to a wire status is decided
// once.
//
// THE DEFAULT IS Unavailable AND NOT Internal, for the reason services/register
// gives: everything not named below came out of the store, and a store failure
// is the caller's cue to retry somewhere else. Internal says "stop, this
// request is broken", which for a replicated store is the wrong instruction.
func wire(err error) error {
	switch {
	case errors.Is(err, ErrNoProject), errors.Is(err, ErrNoEnvironment),
		errors.Is(err, ErrNoName), errors.Is(err, ErrSeparator):
		return connect.NewError(connect.CodeInvalidArgument, err)

	case errors.Is(err, ErrNotSet):
		return connect.NewError(connect.CodeNotFound, err)

	default:
		return connect.NewError(connect.CodeUnavailable, err)
	}
}
