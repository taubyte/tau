package auth

import (
	"context"

	"connectrpc.com/connect"

	authv1 "github.com/taubyte/tau/pkg/v2/proto/gen/auth/v1"
	"github.com/taubyte/tau/pkg/v2/proto/gen/auth/v1/authv1connect"
)

// hookHandler implements authv1connect.HookServiceHandler. See
// repositories.go's repositoryHandler doc for why this is its own type
// rather than a method set on *Service.
//
// NOTHING IN THIS RPC SURFACE CREATES A HOOK. auth.proto declares only Get
// and List for HookService — no Register — because a webhook is created by
// calling GitHub's API with a caller's OAuth token
// (services/auth/github.go's registerGitHubRepository, via
// client.CreatePushHook), and that whole surface is the GitHub-token HTTP
// API this schema deliberately does not model (auth.proto's package doc).
// So on the v2 inter-service RPC surface, hooks are read-only: whatever
// eventually rewrites the GitHub-token HTTP path will write hook rows using
// the same store.go layout this file reads, the way v1's
// registerGitHubRepository calls hooks.NewGithub(...).Register(ctx) directly
// against its own store rather than over p2p. Until that surface exists,
// nothing in this package populates a hook row outside of a test.
type hookHandler struct{ s *Service }

var _ authv1connect.HookServiceHandler = (*hookHandler)(nil)

// Get returns a webhook by id.
func (h *hookHandler) Get(ctx context.Context, req *connect.Request[authv1.HookRef]) (*connect.Response[authv1.Hook], error) {
	id := req.Msg.GetId()
	if err := requireID("id", id); err != nil {
		return nil, err
	}

	provider, err := fetchHookProvider(ctx, h.s.kv, id)
	if err != nil {
		return nil, errNotFound("%w", err)
	}

	switch provider {
	case "github":
		record, err := fetchGithubHook(ctx, h.s.kv, id)
		if err != nil {
			return nil, errInternal("hook %q: %w", id, err)
		}
		return connect.NewResponse(&authv1.Hook{
			Hook: &authv1.Hook_Github{Github: &authv1.GithubHook{
				Id:             id,
				ProviderHookId: record.providerHookID,
				Secret:         record.secret,
				RepositoryKey:  record.repository,
			}},
		}), nil
	default:
		// BitbucketHook is reachable in the schema (auth.proto's Hook
		// oneof) and unreachable in the running service — nothing writes a
		// "bitbucket" provider row anywhere in this package, the same gap
		// v1's own hooks.Fetch has (services/auth/hooks/hooks.go's Fetch:
		// `default: return nil, errors.New("unknown/unsupported git
		// provider " + provider)`).
		return nil, errUnimplemented("hook %q has unsupported provider %q", id, provider)
	}
}

// List streams the id of every stored webhook.
func (h *hookHandler) List(ctx context.Context, _ *connect.Request[authv1.Empty], stream *connect.ServerStream[authv1.HookRef]) error {
	ids, err := listHookIDs(ctx, h.s.kv)
	if err != nil {
		return errInternal("listing hooks: %w", err)
	}
	for _, id := range ids {
		if err := stream.Send(&authv1.HookRef{Id: id}); err != nil {
			return err
		}
	}
	return nil
}
