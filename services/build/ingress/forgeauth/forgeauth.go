// Package forgeauth answers the frontend's provider questions through pkg/forge:
// whether a delivery is genuine and what it means, and the forges' push feeds.
// The forge is the repository key's host in a forge.Registry, so no provider is
// named here. Verification uses only the repository's scheme and secret, never a
// credential.
package forgeauth

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/git/repokey"
	"github.com/taubyte/tau/services/build/ingress"
)

// Forges answers the frontend's provider questions from the forges one
// registry serves.
type Forges struct {
	forges *forge.Registry
}

var (
	_ ingress.Deliveries = (*Forges)(nil)
)

// New returns a Forges over the given registry.
func New(forges *forge.Registry) (*Forges, error) {
	if forges == nil {
		return nil, errors.New("build: forgeauth needs the forges it answers for")
	}
	return &Forges{forges: forges}, nil
}

// Verify turns a delivery into the push it proves, under the hook's stored scheme
// and secret:
//
//   - verification fails, or names another repository -> ErrUnverified
//   - another event (a ping) -> ErrNotAPush
//   - a push deleting its ref -> ErrRefDeleted
//   - a hook this cloud cannot check -> the error itself
//   - anything else (genuine, no usable push) -> ErrMalformed
func (i *Forges) Verify(hook ingress.Hook, header http.Header, body []byte) (ingress.Push, error) {
	if len(hook.Repositories) == 0 {
		return ingress.Push{}, fmt.Errorf("build: project %q has no repositories to verify a push for", hook.Project)
	}

	// Each repository is tried under its own secret, so a delivery verifies only as a
	// push to the repository whose secret signed it.
	last := fmt.Errorf("%w: project %q has no repository with a hook secret", ingress.ErrUnverified, hook.Project)
	for _, repo := range hook.Repositories {
		if len(repo.Secret) == 0 {
			continue
		}
		key, err := repokey.Parse("https://" + repo.Key)
		if err != nil {
			last = fmt.Errorf("build: project %q names no repository %q: %w", hook.Project, repo.Key, err)
			continue
		}
		push, err := i.verifyOne(key, repo.Secret, header, body)
		if err == nil || !errors.Is(err, ingress.ErrUnverified) {
			return push, err
		}
		last = err
	}
	return ingress.Push{}, last
}

func (i *Forges) verifyOne(key repokey.Key, secret []byte, header http.Header, body []byte) (ingress.Push, error) {
	// The scheme is the forge driver's, the same one RegisterPushHook stamped.
	var scheme forge.Scheme
	if driver, err := i.forges.Driver(key.Host); err == nil {
		if sig, ok := driver.(forge.Signatures); ok {
			scheme = sig.Scheme()
		}
	}
	push, err := i.forges.VerifyPush(forge.Hook{
		Key:    key,
		Scheme: scheme,
		Secret: forge.SecretFrom(secret),
	}, forge.Delivery{Header: header, Body: body})

	var (
		unverified    *forge.VerificationError
		noDriver      *forge.NoDriverError
		unsupported   *forge.UnsupportedError
		unaddressable *forge.UnaddressableError
	)
	switch {
	case err == nil:
		return ingress.Push{Repository: push.Key.String(), Ref: push.Ref, Commit: push.Commit}, nil
	case errors.As(err, &unverified):
		return ingress.Push{}, fmt.Errorf("%w: %v", ingress.ErrUnverified, err)
	case errors.Is(err, forge.ErrNotAPush):
		return ingress.Push{}, ingress.ErrNotAPush
	case errors.Is(err, forge.ErrRefDeleted):
		return ingress.Push{}, ingress.ErrRefDeleted
	case errors.As(err, &noDriver), errors.As(err, &unsupported), errors.As(err, &unaddressable):
		return ingress.Push{}, fmt.Errorf("build: %w", err)
	}
	return ingress.Push{}, fmt.Errorf("%w: %v", ingress.ErrMalformed, err)
}
