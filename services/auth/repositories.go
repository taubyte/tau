package auth

import (
	"context"

	"connectrpc.com/connect"

	authv2 "github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2/authv2connect"
)

// repositoryHandler implements authv2connect.RepositoryServiceHandler.
//
// It is its own type, rather than a method set on *Service directly,
// because ProjectServiceHandler, RepositoryServiceHandler and
// HookServiceHandler all declare a `Get` and a `List` method (with different
// message types), and RepositoryServiceHandler and DomainServiceHandler both
// declare `Register` — Go has no method overloading, so one type cannot
// implement more than one of these interfaces at once. Every *Xhandler type
// in this package is this same one-line wrapper around the shared *Service,
// which holds the actual state (rpc.go assembles and mounts all five).
type repositoryHandler struct{ s *Service }

var _ authv2connect.RepositoryServiceHandler = (*repositoryHandler)(nil)

// Get returns a registered repository by its canonical key. It carries the
// deploy key forward unchanged, the way auth.proto's own comment on
// Repository.deploy_key describes it: whether every caller that can reach
// this RPC should keep seeing key material on Get is a real question, but
// restricting it would need to know which OTHER v2 service is meant to read
// a deploy key back out to actually clone with it, and nothing in scope here
// says that is the same peer that called Register — so it is left open
// rather than guessed at, unlike Register/Unregister below, where the
// authorization question has an answer this package can actually check.
func (h *repositoryHandler) Get(ctx context.Context, req *connect.Request[authv2.RepositoryRef]) (*connect.Response[authv2.Repository], error) {
	key, err := parseRepoKey(req.Msg.GetRepositoryKey())
	if err != nil {
		return nil, errInvalidArgument("repository_key: %w", err)
	}

	record, err := fetchRepository(ctx, h.s.kv, key)
	if err != nil {
		return nil, errNotFound("%w", err)
	}

	return connect.NewResponse(&authv2.Repository{
		RepositoryKey: key.String(),
		ProjectId:     record.project,
		DeployKey:     record.deployKey,
	}), nil
}

// List streams the canonical key of every registered repository, at
// whatever depth it happens to be (see store.go's listRepositoryKeys for why
// that is safe against a GitLab subgroup's extra path segment).
func (h *repositoryHandler) List(ctx context.Context, _ *connect.Request[authv2.Empty], stream *connect.ServerStream[authv2.RepositoryRef]) error {
	keys, err := listRepositoryKeys(ctx, h.s.kv)
	if err != nil {
		return errInternal("listing repositories: %w", err)
	}
	for _, key := range keys {
		if err := stream.Send(&authv2.RepositoryRef{RepositoryKey: key}); err != nil {
			return err
		}
	}
	return nil
}

// Register files a fresh deploy key under key and records the calling peer
// as the one allowed to touch it again.
//
// A KEY THAT IS ALREADY REGISTERED IS NOT SILENTLY OVERWRITTEN. v1's
// registerGitHubRepository generates and stores a brand new key pair on
// EVERY call regardless of whether one already existed
// (services/auth/github.go), which is fine on the GitHub-token HTTP surface
// because repoAccess() there re-proves write access to the real repository
// on every call — but that check is `if client != nil` only, so v1's p2p
// path (the one this RPC replaces) skips it entirely and lets any peer
// re-key any repository. Requiring the caller to already own an existing
// registration before Register is allowed to touch it again closes that:
// the FIRST Register on a key is open to any authenticated peer (there is
// nothing yet to own), and every one after that is authz.go's call.
//
// A repeat Register by the peer that already owns the key is a no-op rather
// than a rotation: nothing about this RPC's contract says "issue a new key",
// only "make sure this key is registered", and silently rotating a deploy
// key that some other system may already have been handed would be a worse
// surprise than doing nothing.
func (h *repositoryHandler) Register(ctx context.Context, req *connect.Request[authv2.RepositoryRef]) (*connect.Response[authv2.Empty], error) {
	key, err := parseRepoKey(req.Msg.GetRepositoryKey())
	if err != nil {
		return nil, errInvalidArgument("repository_key: %w", err)
	}

	who, err := caller(ctx)
	if err != nil {
		return nil, err
	}

	if err := authorizeRepositoryOwner(ctx, h.s.kv, key, who); err != nil {
		return nil, err
	}

	if _, registered := repositoryOwner(ctx, h.s.kv, key); registered {
		// authorizeRepositoryOwner already proved it is registered to
		// `who` — a repeat Register by its own owner is a no-op, not a
		// rotation.
		return connect.NewResponse(&authv2.Empty{}), nil
	}

	if err := registerRepository(ctx, h.s.kv, key, who); err != nil {
		return nil, errInternal("registering %q: %w", key, err)
	}

	return connect.NewResponse(&authv2.Empty{}), nil
}

// Unregister removes a repository and every row it owns.
//
// V1 DOES NOT LEAVE /project DANGLING, and this package should not be read
// as fixing that: commit 3a2111da deletes it in the same batch as /key
// (services/auth/repositories/repositories.go:65) and cascades hooks
// through `for _, h := range r.Hooks(ctx)` in
// services/auth/github.go's unregisterGitHubRepository. What is actually
// different here is two things and only two things: the cascade is ONE
// batch instead of N+1 separate deletes, so a failure partway through
// cannot leave some hooks gone and others not; and /registered_by — the row
// that makes this RPC checkable at all — is a v2 row, so v1 has nothing to
// tear down in its place.
func (h *repositoryHandler) Unregister(ctx context.Context, req *connect.Request[authv2.RepositoryRef]) (*connect.Response[authv2.Empty], error) {
	key, err := parseRepoKey(req.Msg.GetRepositoryKey())
	if err != nil {
		return nil, errInvalidArgument("repository_key: %w", err)
	}

	who, err := caller(ctx)
	if err != nil {
		return nil, err
	}

	if _, err := fetchRepository(ctx, h.s.kv, key); err != nil {
		return nil, errNotFound("%w", err)
	}

	if err := authorizeRepositoryOwner(ctx, h.s.kv, key, who); err != nil {
		return nil, err
	}

	if err := deleteRepository(ctx, h.s.kv, key); err != nil {
		return nil, errInternal("unregistering %q: %w", key, err)
	}

	return connect.NewResponse(&authv2.Empty{}), nil
}
