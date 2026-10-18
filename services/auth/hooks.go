package auth

import (
	"context"
	"errors"

	"connectrpc.com/connect"

	authv2 "github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2/authv2connect"
)

// hookHandler implements authv2connect.HookServiceHandler. See
// repositories.go's repositoryHandler doc for why this is its own type
// rather than a method set on *Service.
//
// NOTHING IN THIS RPC SURFACE CREATES A HOOK. auth.proto declares only Get
// and List for HookService: a webhook is created on the FORGE, with a
// credential that may create one there, and filed here through store.go's
// putHook by whatever registers the repository.
type hookHandler struct{ s *Service }

var _ authv2connect.HookServiceHandler = (*hookHandler)(nil)

// Get returns a webhook by id. There is no per-provider case: the host in the
// repository key is the forge, and the scheme stored with the hook is how its
// deliveries are checked.
func (h *hookHandler) Get(ctx context.Context, req *connect.Request[authv2.HookRef]) (*connect.Response[authv2.Hook], error) {
	id := req.Msg.GetId()
	if err := requireID("id", id); err != nil {
		return nil, err
	}

	record, err := fetchHook(ctx, h.s.kv, id)
	if errors.Is(err, errNoHook) {
		return nil, errNotFound("%w", err)
	}
	if err != nil {
		return nil, errInternal("%w", err)
	}
	return connect.NewResponse(&authv2.Hook{
		Id:            id,
		RepositoryKey: record.repository,
		Handle:        record.handle,
		Scheme:        record.scheme,
		Secret:        []byte(record.secret),
	}), nil
}

// List streams the id of every stored webhook.
func (h *hookHandler) List(ctx context.Context, _ *connect.Request[authv2.Empty], stream *connect.ServerStream[authv2.HookRef]) error {
	ids, err := listHookIDs(ctx, h.s.kv)
	if err != nil {
		return errInternal("listing hooks: %w", err)
	}
	for _, id := range ids {
		if err := stream.Send(&authv2.HookRef{Id: id}); err != nil {
			return err
		}
	}
	return nil
}
