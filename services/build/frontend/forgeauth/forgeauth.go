// Package forgeauth answers the frontend's provider questions through
// pkg/forge: whether a webhook delivery is genuine and what it means
// (frontend.Deliveries), and the forges' own push feeds (frontend.Streams).
//
// # The host is the provider
//
// Which forge answers is the repository key's host, looked up in a
// forge.Registry the node built from what it serves. GitHub, GitLab, an
// enterprise instance and a mirror are bindings in that registry, not branches
// here: this package names no provider and imports no provider SDK.
//
// # It verifies with no credential at all
//
// A delivery is verified with the repository's own scheme and secret and
// nothing else — Registry.VerifyPush takes no credential, so the process a
// stranger's POST reaches cannot degrade into an API call.
package forgeauth

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/git/repokey"
	"github.com/taubyte/tau/services/build/frontend"
)

// Forges answers the frontend's provider questions from the forges one
// registry serves.
type Forges struct {
	forges *forge.Registry
}

var (
	_ frontend.Deliveries = (*Forges)(nil)
)

// New returns a Forges over the given registry.
func New(forges *forge.Registry) (*Forges, error) {
	if forges == nil {
		return nil, errors.New("build: forgeauth needs the forges it answers for")
	}
	return &Forges{forges: forges}, nil
}

// Verify turns a delivery into the push it proves, under the scheme and secret
// stored with the hook, through the forge serving the hook's host.
//
// The four ways a genuine delivery is not a build are told apart, because
// frontend/webhook.go answers each with its own status and only one of them —
// a delivery that is not genuine — is an attack:
//
//   - a verification failure -> ErrUnverified, including a delivery naming a
//     repository other than the hook's own
//   - another event (a ping when the hook is created) -> ErrNotAPush
//   - a push deleting its ref -> ErrRefDeleted
//   - a hook this cloud cannot check at all (no forge for its host, a forge
//     that reads no deliveries, a key it cannot address) -> the error itself
//   - anything else, which is a genuine delivery naming no usable push ->
//     ErrMalformed
func (i *Forges) Verify(hook frontend.Hook, header http.Header, body []byte) (frontend.Push, error) {
	if len(hook.Repositories) == 0 {
		return frontend.Push{}, fmt.Errorf("build: project %q has no repositories to verify a push for", hook.Project)
	}

	// EACH REPOSITORY IS TRIED UNDER ITS OWN SECRET, because the URL names the
	// project and only the verified body names which of its repositories was
	// pushed. Every attempt is the whole of VerifyPush — the signature and the
	// repository check together — so a delivery verifies only as a push to the
	// repository whose secret signed it.
	last := fmt.Errorf("%w: project %q has no repository with a hook secret", frontend.ErrUnverified, hook.Project)
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
		if err == nil || !errors.Is(err, frontend.ErrUnverified) {
			return push, err
		}
		last = err
	}
	return frontend.Push{}, last
}

func (i *Forges) verifyOne(key repokey.Key, secret []byte, header http.Header, body []byte) (frontend.Push, error) {
	// THE SCHEME IS THE FORGE'S, read from the driver that serves the host, and
	// it is the same value forge.Session.RegisterPushHook stamped when the hook
	// was installed — so what a hook was signed with and what it is checked
	// against cannot drift apart.
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
		return frontend.Push{Repository: push.Key.String(), Ref: push.Ref, Commit: push.Commit}, nil
	case errors.As(err, &unverified):
		return frontend.Push{}, fmt.Errorf("%w: %v", frontend.ErrUnverified, err)
	case errors.Is(err, forge.ErrNotAPush):
		return frontend.Push{}, frontend.ErrNotAPush
	case errors.Is(err, forge.ErrRefDeleted):
		return frontend.Push{}, frontend.ErrRefDeleted
	case errors.As(err, &noDriver), errors.As(err, &unsupported), errors.As(err, &unaddressable):
		return frontend.Push{}, fmt.Errorf("build: %w", err)
	}
	return frontend.Push{}, fmt.Errorf("%w: %v", frontend.ErrMalformed, err)
}
