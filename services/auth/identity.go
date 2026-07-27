//go:build !ee

package auth

import (
	"context"
	"errors"
	"fmt"

	tauConfig "github.com/taubyte/tau/pkg/config"
	http "github.com/taubyte/tau/pkg/http"
)

// initIdentity binds this service to the namespace that owns the cloud.
//
// Outside dev mode a tenancy is required, not optional: without one there is no
// answer to "may this identity use this cloud", and starting anyway would serve
// an API to anyone holding a valid provider token. Dev mode is exempt so local
// and dream clouds need no namespace.
func (srv *AuthService) initIdentity(cfg tauConfig.Config) error {
	srv.tenancy = cfg.Tenancy()

	if !srv.devMode && !srv.tenancy.Configured() {
		return ErrNoTenancy
	}

	var err error
	srv.membership, err = tenancyVerifier(srv.tenancy)
	return err
}

// authorizeIdentity answers whether the caller may use this cloud's API. This
// build answers from the configured namespace: members may, everyone else may
// not. There is no accounts service to ask.
func (srv *AuthService) authorizeIdentity(rctx context.Context, _ http.Context, client GitHubClient) error {
	if srv.devMode {
		return nil
	}

	// initIdentity refuses to start without these, so neither is reachable.
	// They stay because the alternative here is a nil-deref panic on an
	// authorization path rather than a refusal.
	if !srv.tenancy.Configured() {
		return ErrNoTenancy
	}
	if srv.membership == nil {
		return errors.New("membership verifier unavailable")
	}

	me := client.Me()
	if me == nil || me.GetLogin() == "" {
		return errors.New("github user identity unavailable")
	}

	member, err := srv.membership.IsActiveMember(rctx, srv.tenancy.Owner, me.GetLogin())
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
