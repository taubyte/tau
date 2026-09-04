package backend

import (
	"context"

	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/taubyte/tau/p2p/rpc"
)

// authz.go is the only file in this package that reads caller identity
// (service-conventions.md §5b: "an identity that is optional to read is an
// identity that is not checked").
//
// It reads rpc.RemotePeer(ctx), which go-libp2p-gostream fills in from the
// security-authenticated stream the request arrived on, and never a field in
// the request message — a request field is what the caller itself wrote.
// build.proto says so where it says why JobService carries no caller field:
// "the identity on a p2p stream is the calling node's, and a frontend relaying
// four HTTP routes is one node relaying many humans", so a field there would be
// exactly the identity-the-caller-encodes that §5b exists to refuse.
//
// EVERY RPC REQUIRES AN IDENTITY, and that is not tautological. rpc.RemotePeer
// returns ErrNoRemotePeer for a request that did not arrive over one of
// p2p/rpc's listeners, so a handler that calls allow() is structurally
// unmountable on an HTTP ingress — which is the one mistake this whole trust
// boundary is about. A build-frontend that grew a route straight onto these
// handlers would fail every call rather than serve the queue to the internet.
//
// THE DEFAULT IS THAT ANY PEER THE TRANSPORT AUTHENTICATED MAY CALL, and the
// membership gap under it is named rather than papered over: per
// service-conventions.md §5c(1) there is no allowlist, no connection gater and
// no join policy anywhere in this tree, and pkg/config/source_load.go's
// loadSwarmKey("") returns nil with no error, so an omitted swarmkey yields an
// open libp2p network. That is the documented OSS trust model, and this package
// does NOT build a second membership system in front of it: doing so would be
// exactly the service-local admission policy service-conventions.md §4 reserves
// for the cluster service. AllowPeers exists so a deployment that knows its
// frontends' peer ids can narrow ActEnqueue without inventing a mechanism.
func caller(ctx context.Context) (peer.ID, error) {
	id, err := rpc.RemotePeer(ctx)
	if err != nil {
		return "", errUnauthenticated(err)
	}
	return id, nil
}

// allow resolves the caller and asks the injected Authorizer. Every one of the
// five RPCs calls it, first, before anything else reads the request.
func (s *Service) allow(ctx context.Context, act Action, subject string) error {
	id, err := caller(ctx)
	if err != nil {
		return err
	}
	if err := s.authz.Allow(ctx, id, act, subject); err != nil {
		return err
	}
	return nil
}
