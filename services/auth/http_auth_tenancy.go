package auth

import (
	"errors"
	"fmt"

	http "github.com/taubyte/tau/pkg/http"
)

// GitHubTokenHTTPAuthRegistration gates the routes that add something to this
// cloud: creating a project, importing one, registering or unregistering a
// repository. It validates the token the same way every other route does, then
// requires the caller to belong to the configured namespace.
//
// The routes that only read are deliberately not behind this. Membership
// answers "may you register here", and re-asking it on a read would revoke
// access to a repository this cloud already accepted.
func (srv *AuthService) GitHubTokenHTTPAuthRegistration(ctx http.Context) (interface{}, error) {
	if _, err := srv.GitHubTokenHTTPAuth(ctx); err != nil {
		return nil, err
	}

	if err := srv.authorizeRegistrant(ctx); err != nil {
		// The token client is opened by GitHubTokenHTTPAuth and released by its
		// GC handler, which the router does not run when a validator fails.
		srv.GitHubTokenHTTPAuthCleanup(ctx)
		return nil, err
	}

	return nil, nil
}

func (srv *AuthService) authorizeRegistrant(ctx http.Context) error {
	if srv.devMode {
		return nil
	}

	if !srv.tenancy.Configured() {
		return ErrNoTenancy
	}

	// Startup refuses a configured tenancy without one, so this is unreachable.
	// It stays because the alternative on an authorization path is a nil-deref
	// panic rather than a refusal.
	if srv.membership == nil {
		return errors.New("membership verifier unavailable")
	}

	client, err := getGithubClientFromContext(ctx)
	if err != nil {
		return err
	}

	me := client.Me()
	if me == nil || me.GetLogin() == "" {
		return errors.New("github user identity unavailable")
	}

	member, err := srv.membership.IsActiveMember(ctx.Request().Context(), srv.tenancy.Owner, me.GetLogin())
	if err != nil {
		// Relayed as-is. A check that never ran is not a refusal, and the
		// provider's own message is the only thing that says what to fix.
		return err
	}
	if !member {
		return fmt.Errorf("`%s` is not a member of `%s`", me.GetLogin(), srv.tenancy.Owner)
	}

	return nil
}

// authorizeRepository refuses a repository owned by anyone other than the
// configured namespace. The owner is already in hand from the fetch, so this
// costs a string comparison and no API call.
func (srv *AuthService) authorizeRepository(client GitHubClient) error {
	if srv.devMode {
		return nil
	}

	if !srv.tenancy.Configured() {
		return ErrNoTenancy
	}

	repo := client.Cur()
	if repo == nil {
		return errors.New("no repository selected")
	}

	fullName := repo.GetFullName()
	if !srv.tenancy.Owns(fullName) {
		return fmt.Errorf("repository `%s` is not owned by `%s`", fullName, srv.tenancy.Owner)
	}

	return nil
}
