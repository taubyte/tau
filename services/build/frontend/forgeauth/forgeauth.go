// Package forgeauth answers the frontend's two provider questions through
// pkg/forge: what a caller's token may do to a repository (frontend.Identity),
// and whether a webhook delivery is genuine and what it means
// (frontend.Deliveries).
//
// # The host is the provider
//
// Which forge answers is the repository key's host, looked up in a
// forge.Registry the node built from what it serves. GitHub, GitLab, an
// enterprise instance and a mirror are bindings in that registry, not branches
// here: this package names no provider and imports no provider SDK.
//
// # It asks under the caller's own token, and verifies with no credential at all
//
// The access question is asked with the CALLER's credential, so it cannot be
// answered by a token this cloud holds instead, and a token this cloud has not
// stored does not become one it has. A delivery is verified with the hook's own
// scheme and secret and nothing else — Registry.VerifyPush takes no credential,
// so the process a stranger's POST reaches cannot degrade into an API call.
package forgeauth

import (
	"context"
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
	_ frontend.Identity   = (*Forges)(nil)
	_ frontend.Deliveries = (*Forges)(nil)
)

// New returns a Forges over the given registry.
func New(forges *forge.Registry) (*Forges, error) {
	if forges == nil {
		return nil, errors.New("build: forgeauth needs the forges it answers for")
	}
	return &Forges{forges: forges}, nil
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
func (i *Forges) WriteAccess(ctx context.Context, token string, key repokey.Key) (frontend.Access, error) {
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

	// EACH OF THE PROJECT'S REPOSITORIES IS TRIED, because the URL names the
	// project and only the verified body names which of its repositories was
	// pushed. Every attempt is the whole of VerifyPush — the signature and the
	// repository check together — so a delivery verifies only as a push to a
	// repository the project owns. A delivery whose signature is good for
	// none of them is unverified; one that names a repository outside the
	// project is the same.
	var last error
	for _, spelling := range hook.Repositories {
		key, err := repokey.Parse("https://" + spelling)
		if err != nil {
			last = fmt.Errorf("build: project %q names no repository %q: %w", hook.Project, spelling, err)
			continue
		}
		push, err := i.verifyOne(key, hook.Secret, header, body)
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

// Claimant is the token holder's stable identity on host.
func (i *Forges) Claimant(ctx context.Context, token, host string) (string, error) {
	if token == "" {
		return "", frontend.ErrBadCredential
	}
	session, err := i.forges.Host(host, forge.Token(token))
	var noDriver *forge.NoDriverError
	if errors.As(err, &noDriver) {
		return "", fmt.Errorf("%w: %s", frontend.ErrUnknownForge, host)
	}
	if err != nil {
		return "", fmt.Errorf("build: %w", err)
	}
	id, err := session.Whoami(ctx)
	var refused *forge.CredentialError
	switch {
	case errors.As(err, &refused):
		return "", frontend.ErrBadCredential
	case err != nil:
		return "", fmt.Errorf("build: asking %s who this is: %w", host, err)
	case id.Subject == "":
		return "", fmt.Errorf("build: %s answered with no subject", host)
	}
	return session.Host() + "/" + id.Subject, nil
}
