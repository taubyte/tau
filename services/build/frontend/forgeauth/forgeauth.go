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
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"

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

var _ frontend.Installer = (*Forges)(nil)

// installing is token's session on key's forge, able to install what a link
// installs.
func (i *Forges) installing(key repokey.Key, token string) (*forge.Session, error) {
	session, err := i.forges.For(key, forge.Token(token))
	if err != nil {
		return nil, fmt.Errorf("build: %w", err)
	}
	if err := session.Require(forge.CapPushHooks, forge.CapDeployKeys); err != nil {
		return nil, fmt.Errorf("%w: %v", frontend.ErrCannotInstall, err)
	}
	return session, nil
}

// Inspect reads key the way a link needs to before it claims anything.
func (i *Forges) Inspect(ctx context.Context, token string, key repokey.Key, callback *url.URL) (frontend.Inspection, error) {
	session, err := i.installing(key, token)
	if err != nil {
		return frontend.Inspection{}, err
	}
	// THE FORGE'S SPELLING, because a delivery names the repository the way
	// the forge does and the cloud compares it exactly.
	repo, err := session.Repository(ctx, key)
	if err != nil {
		return frontend.Inspection{}, fmt.Errorf("build: reading %s on its forge: %w", key, err)
	}
	access, err := session.Access(ctx, repo.Key)
	if err != nil {
		return frontend.Inspection{}, fmt.Errorf("build: reading the access to %s: %w", key, err)
	}
	out := frontend.Inspection{Repository: repo.Key.String(), Admin: access.CanAdmin()}
	if !out.Admin {
		// Listing hooks takes admin too; the link stops on Admin anyway.
		return out, nil
	}
	hooks, err := hooksTo(ctx, session, repo.Key, callback)
	if err != nil {
		return frontend.Inspection{}, err
	}
	out.Hooked = len(hooks) > 0
	return out, nil
}

// Install puts a project's webhook and a deploy key on key.
func (i *Forges) Install(ctx context.Context, token string, key repokey.Key, in frontend.Install) error {
	session, err := i.installing(key, token)
	if err != nil {
		return err
	}
	access, err := session.Access(ctx, key)
	if err != nil {
		return fmt.Errorf("build: reading the access to %s: %w", key, err)
	}
	if !access.CanAdmin() {
		return fmt.Errorf("%w: the account holds %s on %s", frontend.ErrNotAdmin, access, key)
	}
	hooks, err := hooksTo(ctx, session, key, in.Callback)
	if err != nil {
		return err
	}
	if len(hooks) > 0 && !in.Replace {
		return fmt.Errorf("%w: %s", frontend.ErrAlreadyHooked, key)
	}
	if len(hooks) > 0 {
		for _, h := range hooks {
			if err := session.RemovePushHook(ctx, key, h); err != nil {
				return fmt.Errorf("build: deleting the previous webhook on %s: %w", key, err)
			}
		}
		installed, err := session.ListDeployKeys(ctx, key)
		if err != nil {
			return fmt.Errorf("build: reading the deploy keys on %s: %w", key, err)
		}
		for _, k := range installed {
			if k.Title != in.Title {
				continue
			}
			if err := session.RemoveDeployKey(ctx, key, k.Handle); err != nil {
				return fmt.Errorf("build: deleting the previous deploy key on %s: %w", key, err)
			}
		}
	}
	if _, err := session.RegisterPushHook(ctx, key, in.Callback, forge.SecretFrom(in.Secret)); err != nil {
		return fmt.Errorf("build: installing the webhook on %s: %w", key, err)
	}
	if _, err := session.InstallDeployKey(ctx, key, in.Title, in.DeployKey); err != nil {
		return fmt.Errorf("build: installing the deploy key on %s: %w", key, err)
	}
	return nil
}

// hooksTo are the hooks on key that deliver to callback.
func hooksTo(ctx context.Context, session *forge.Session, key repokey.Key, callback *url.URL) ([]forge.Handle, error) {
	hooks, err := session.ListPushHooks(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("build: reading the webhooks on %s: %w", key, err)
	}
	var out []forge.Handle
	for _, h := range hooks {
		if h.Callback == callback.String() {
			out = append(out, h.Handle)
		}
	}
	return out, nil
}

var _ frontend.Repositories = (*Forges)(nil)

// forgeErr says a pkg/forge refusal in the frontend's terms.
func forgeErr(err error) error {
	var (
		unsupported *forge.UnsupportedError
		noDriver    *forge.NoDriverError
		refused     *forge.CredentialError
		exists      *forge.ExistsError
	)
	switch {
	case errors.As(err, &exists):
		return fmt.Errorf("%w: %v", frontend.ErrExists, err)
	case errors.As(err, &unsupported):
		return fmt.Errorf("%w: %v", frontend.ErrUnsupported, err)
	case errors.As(err, &noDriver):
		return fmt.Errorf("%w: %s", frontend.ErrUnknownForge, noDriver.Host)
	case errors.As(err, &refused):
		return frontend.ErrBadCredential
	}
	return fmt.Errorf("build: %w", err)
}

func info(r forge.Repository) frontend.RepositoryInfo {
	return frontend.RepositoryInfo{Repository: r.Key.String(), DefaultBranch: r.DefaultBranch, Private: r.Private}
}

// maxListed bounds one listing; every forge pages and none bounds the result.
const maxListed = 1000

// List is what the holder of token reaches on host.
func (i *Forges) List(ctx context.Context, token, host string) ([]frontend.RepositoryInfo, error) {
	session, err := i.forges.Host(host, forge.Token(token))
	if err != nil {
		return nil, forgeErr(err)
	}
	out := []frontend.RepositoryInfo{}
	err = session.ListRepositories(ctx, func(r forge.Repository) bool {
		out = append(out, info(r))
		return len(out) < maxListed
	})
	if err != nil {
		return nil, forgeErr(err)
	}
	return out, nil
}

// Create makes key on its forge, as the holder of token.
func (i *Forges) Create(ctx context.Context, token string, key repokey.Key, in frontend.NewRepository) (frontend.RepositoryInfo, error) {
	session, err := i.forges.For(key, forge.Token(token))
	if err != nil {
		return frontend.RepositoryInfo{}, forgeErr(err)
	}
	made, err := session.Create(ctx, key, forge.CreateOptions{Description: in.Description, Private: in.Private})
	if err != nil {
		return frontend.RepositoryInfo{}, forgeErr(err)
	}
	return info(made), nil
}

// ReadAccess reports whether the holder of token may read key, with
// WriteAccess's three answers kept three.
func (i *Forges) ReadAccess(ctx context.Context, token string, key repokey.Key) (frontend.Access, error) {
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
	case access >= forge.AccessRead:
		return frontend.AccessGranted, nil
	}
	return frontend.AccessDenied, nil
}

// Remote is key's https clone address on its forge, reached through a
// transport that attaches token, as git's basic auth, to that host alone.
func (i *Forges) Remote(ctx context.Context, token string, key repokey.Key) (frontend.Remote, error) {
	session, err := i.forges.For(key, forge.Token(token))
	if err != nil {
		return frontend.Remote{}, forgeErr(err)
	}
	addr, err := session.CloneAddress(key, forge.HTTPS)
	if err != nil {
		return frontend.Remote{}, forgeErr(err)
	}
	u, err := url.Parse(addr.URL)
	switch {
	case err != nil:
		return frontend.Remote{}, fmt.Errorf("build: %s answered a clone address that is not a url: %w", key.Host, err)
	case u.User != nil:
		return frontend.Remote{}, fmt.Errorf("build: %s answered a clone address carrying a credential", key.Host)
	case addr.HTTPUsername == "":
		return frontend.Remote{}, fmt.Errorf("build: %s answered no account to authenticate the clone of %s as", key.Host, key)
	}
	who, err := session.Whoami(ctx)
	if err != nil {
		return frontend.Remote{}, forgeErr(err)
	}
	email := who.Email
	if email == "" {
		email = who.Login + "@" + key.Host
	}
	basic := "Basic " + base64.StdEncoding.EncodeToString([]byte(addr.HTTPUsername+":"+token))
	return frontend.Remote{
		URL:       u,
		Transport: forge.PinnedTransport(key.Host, nil, func(h http.Header) { h.Set("Authorization", basic) }),
		Author:    who.Login,
		Email:     email,
	}, nil
}
