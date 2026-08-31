package auth

import (
	"context"

	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/git/repokey"
)

// authz.go is the only file in this package that reads caller identity
// (service-conventions.md §5b: "an identity that is optional to read is an
// identity that is not checked"). Every handler that needs to know who is
// calling goes through caller() below, never through a field in the request
// message — a request field is exactly what the caller itself wrote, so a
// caller wanting to be someone else would just write a different value in
// it. caller() instead reads rpc.RemotePeer(ctx), which is filled in by
// go-libp2p-gostream from the security-authenticated stream the request
// arrived on and cannot be set by anything the caller sends (p2p/rpc's own
// TestRemotePeerCannotBeForged proves this at the transport layer).
//
// THE DEFAULT, FOR EVERYTHING NOT NAMED BELOW, IS THAT ANY PEER THE
// TRANSPORT AUTHENTICATED MAY CALL. Per service-conventions.md §5c(1), "is
// this peer a member of the swarm" has no mechanism anywhere in this tree —
// no allowlist, no connection gater, no join policy — and an omitted
// swarmkey yields an open libp2p network with no error
// (pkg/config/source_load.go:147-150). That is the documented OSS trust
// model (a peer that reached this node over the swarm is, by that fact,
// trusted the way any other swarm peer is), and this package does not
// invent a second membership system to sit in front of it — building one
// here would be exactly the kind of service-local admission policy
// service-conventions.md §4 reserves for the (not-yet-built) cluster
// service. So ProjectService, HookService, DomainService and
// CertificateService below carry no authz check at all: every RPC they
// expose is either a read or a metadata write with no secret attached, and
// is reachable to any authenticated peer, matching v1's own p2p surface
// (services/auth/api_hooks.go's apiProjectsServiceHandler, e.g., runs
// createProjectStream for any caller with no check of any kind).
//
// REPOSITORY REGISTRATION IS THE ONE EXCEPTION, because it is the one RPC
// that manages a SECRET with a lifecycle: a deploy key is private key
// material, generated once and meant to be trusted by exactly the systems
// that were handed it. v1's own p2p path for this — registerRepositoryStream
// / unregisterRepositoryStream in services/auth/api_hooks.go — calls
// registerGitHubRepository / unregisterGitHubRepository with a nil
// GitHubClient, and both functions gate their only authorization check
// (repoAccess + authorizeRepository) behind `if client != nil`
// (services/auth/github.go). So on v1's p2p surface, ANY peer that can open
// a stream to auth may register a repository under any key, or tear down
// and re-key one someone else already registered — the write-access check
// only exists for the GitHub-token HTTP surface, which is out of scope here
// (auth.proto's package doc). That gap is not carried forward: since a
// GitHub token cannot be checked at this layer, the transport's own peer
// identity is the substitute — whichever peer registered a repository is
// the only one who may register over it again or unregister it.
func caller(ctx context.Context) (peer.ID, error) {
	id, err := rpc.RemotePeer(ctx)
	if err != nil {
		return "", errUnauthenticated(err)
	}
	return id, nil
}

// authorizeRepositoryOwner governs both Register (on a key that is already
// registered — a fresh key is open to any authenticated peer, first writer
// wins, matching the OSS default above) and Unregister. It returns nil when
// key was never registered, or when it was and by registered it;
// PermissionDenied otherwise.
//
// A NEVER-REGISTERED KEY IS NOT DENIED HERE. Unregister on one still fails —
// deleteRepository is never reached, because repositories.go's own Unregister
// checks existence first and returns NotFound — but that is a "there is
// nothing to authorize" answer, not a "you may not do this" one, and
// conflating the two would tell an unrelated caller that a key IS registered
// (just not to them) purely from the shape of the error it gets back.
func authorizeRepositoryOwner(ctx context.Context, kv Store, key repokey.Key, by peer.ID) error {
	if owner, registered := repositoryOwner(ctx, kv, key); registered && owner != by {
		return errPermissionDenied("repository %q is registered to another peer", key)
	}
	return nil
}
