// Package forgeauth resolves a caller's forge token into what its holder may do
// with a repository. It is the production implementation of
// services/build/frontend's Identity.
//
// # The host is the provider
//
// Which forge answers is the repository key's host, looked up in a
// forge.Registry the node built from what it serves. GitHub, GitLab, an
// enterprise instance and a mirror are bindings in that registry, not branches
// here: this package names no provider and imports no provider SDK.
//
// # It asks under the caller's own token
//
// The access question is asked with the CALLER's credential, so it cannot be
// answered by a token this cloud holds instead, and a token this cloud has not
// stored does not become one it has.
package forgeauth

import (
	"context"
	"errors"
	"fmt"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/git/repokey"
	"github.com/taubyte/tau/services/build/frontend"
)

// Identity answers frontend.Identity from the forges one registry serves.
type Identity struct {
	forges *forge.Registry
}

var _ frontend.Identity = (*Identity)(nil)

// New returns an Identity over the given forges.
func New(forges *forge.Registry) (*Identity, error) {
	if forges == nil {
		return nil, errors.New("build: forgeauth needs the forges it answers for")
	}
	return &Identity{forges: forges}, nil
}

// WriteAccess reports whether the holder of token may write to key.
//
// WRITE, NOT READ: a public repository reads to everyone, which is what let an
// unrelated caller reach another project's repositories in v1.
//
// The three answers are kept three, because frontend/authz.go answers a denial
// with a 404 and a failure with a 502, and an outage that became "not yours"
// would be a support call rather than a bug report:
//
//   - a verdict -> granted or denied, with a nil error
//   - the credential refused -> AccessUnauthenticated; the caller can fix that
//   - no repository the token can see -> denied; "does not exist" and "you may
//     not see it" are one fact from here, which keeps this from being an
//     existence oracle
//   - anything else, including a host no forge is bound to -> an error. A host
//     this cloud does not serve is not a denial: answering "denied" would be a
//     lie that looks like a decision.
func (i *Identity) WriteAccess(ctx context.Context, token string, key repokey.Key) (frontend.Access, error) {
	if token == "" {
		return frontend.AccessUnauthenticated, nil
	}

	session, err := i.forges.For(key, forge.Token(token))
	if err != nil {
		return frontend.AccessDenied, fmt.Errorf("build: %w", err)
	}

	access, err := session.Access(ctx, key)
	var refused *forge.CredentialError
	var missing *forge.NotFoundError
	switch {
	case errors.As(err, &refused):
		return frontend.AccessUnauthenticated, nil
	case errors.As(err, &missing):
		return frontend.AccessDenied, nil
	case err != nil:
		return frontend.AccessDenied, fmt.Errorf("build: asking %s: %w", key.Host, err)
	case access.CanWrite():
		return frontend.AccessGranted, nil
	}
	return frontend.AccessDenied, nil
}
