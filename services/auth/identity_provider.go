package auth

import (
	"context"

	iface "github.com/taubyte/tau/core/services/auth"
	http "github.com/taubyte/tau/pkg/http"
)

// identityProvider answers whether a caller may use this cloud, for builds that
// answer it by asking something rather than by reading configuration. Nil in
// the build that answers from the tenancy, which has nothing to ask.
//
// Stated here as what this service needs, so no build has to name a type only
// one of them provides. Whatever satisfies it is chosen in initIdentity.
type identityProvider interface {
	Authorize(ctx context.Context, rctx http.Context, caller iface.Caller) error
	AuthorizeRepository(caller iface.Caller) error
	Close()
}
