package frontend

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/taubyte/tau/pkg/git/repokey"
	buildv2 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v2"
)

// authz.go is the only file in this package that decides whether a caller may
// do a thing, and it decides it ON THE RESOURCE rather than on the credential.
//
// THAT IS THE WHOLE DIFFERENCE FROM v1. patrick's four job routes are guarded
// by GitHubTokenHTTPAuth, which proves that the bearer holds a token GitHub
// still answers for — and then no handler on any of those routes ever reads the
// resulting client. So the check establishes "you are somebody on GitHub", and
// the handler acts as if it had established "you are somebody on THIS project":
// any GitHub account can read, cancel or retry any project's jobs.
//
// The rule this package applies instead is not invented here. It is
// services/auth/github.go:116-132's own repoAccess — admin|maintain|push, write
// and not read, "because a public repository reads to everyone" — restated at
// the second place that needs it. Stating an existing decision where it is
// needed is not making one.
//
// A DENIAL IS A 404 (errors.go), so none of these routes is an id oracle.

const bearerPrefix = "Bearer "

// THE ONE CREDENTIAL IS THE TAU TOKEN, in Authorization. The forge
// credential a route asks a forge with is not sent by the caller: it is one of
// the connections sealed inside the tau token, opened by auth for the
// repository's host (Connections). So a project whose repositories live on
// two forges is reached with one token holding both.

// RenewedTokenHeader is the response header a renewed tau token comes back
// in, when a connection's forge credential had expired and auth renewed it
// during the request. A client replaces its access token with it.
const RenewedTokenHeader = "Tau-Renewed-Token"

// opened is one request's forge credentials, opened once per host.
type opened struct {
	header http.Header
	tau    string
	hosts  map[string]string
}

type openedKey struct{}

// withOpened gives a request somewhere to keep what it opened and to hand a
// renewed token back.
func withOpened(w http.ResponseWriter, r *http.Request, tau string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), openedKey{}, &opened{header: w.Header(), tau: tau, hosts: map[string]string{}}))
}

// forgeToken is the caller's forge credential for host, from the
// connection their tau token carries.
func (s *Service) forgeToken(ctx context.Context, tau, host string) (string, error) {
	o, _ := ctx.Value(openedKey{}).(*opened)
	if o != nil && o.tau == tau {
		if f, ok := o.hosts[host]; ok {
			return f, nil
		}
	}
	got, err := s.connections.Open(ctx, tau, host)
	if err != nil {
		return "", err
	}
	if o != nil && o.tau == tau {
		o.hosts[host] = got.Forge
		if got.Renewed != "" {
			o.header.Set(RenewedTokenHeader, got.Renewed)
		}
	}
	return got.Forge, nil
}

// notConnected is the answer to a caller whose token holds no usable
// connection to a host the request needs: a 401, which they fix by
// connecting it.
func notConnected(err error) error {
	var nc *NotConnectedError
	if errors.As(err, &nc) {
		return problem(http.StatusUnauthorized, nc.Error())
	}
	return nil
}

// maxIDLength bounds a caller-chosen path segment before it leaves this
// process. The backend refuses anything that is not a 43-character job id, but
// it should not have to receive a megabyte to say so.
const maxIDLength = 128

// bearer reads the credential. It is deliberately strict about the scheme: a
// token accepted from a query parameter or a cookie is a token that ends up in
// an access log or is sent by a browser the caller did not mean to authorize.
func bearer(r *http.Request) (string, error) {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), bearerPrefix)
	if !ok || strings.TrimSpace(token) == "" {
		return "", errUnauthorized
	}
	return token, nil
}

// pathID reads one bounded path value.
func pathID(r *http.Request, name string) (string, error) {
	id := r.PathValue(name)
	if id == "" || len(id) > maxIDLength {
		return "", errDenied
	}
	return id, nil
}

// mayTouchProject authorizes a project-scoped route.
//
// The subject is the project, and what gates it is the project table's answer about
// which repositories the project is made of — the same scoping
// services/auth/github.go's authorizeProject applies when it lists projects, so
// a project is reachable by id to exactly the people who already see it in
// their own list.
func (s *Service) mayTouchProject(ctx context.Context, token, projectID string) error {
	keys, err := s.projects.Repositories(ctx, projectID)
	if err != nil {
		// An unknown project and an unreachable register are NOT the same answer,
		// and translate keeps them apart: NotFound becomes the same 404 a
		// denial does, and a transport failure becomes a 502.
		return err
	}
	return s.writesAny(ctx, token, keys)
}

// mayTouchJob authorizes a job-scoped route and hands back the job it had to
// read to do it, so no route reads the same record twice.
//
// The job's own repository_key is the subject: a job belongs to a repository,
// and whoever may write to that repository may see and stop its builds.
func (s *Service) mayTouchJob(ctx context.Context, token, jobID string) (*buildv2.Job, error) {
	job, err := s.backend.Get(ctx, jobID)
	if err != nil {
		return nil, err
	}
	if err := s.writesAny(ctx, token, []string{job.GetRepositoryKey()}); err != nil {
		return nil, err
	}
	return job, nil
}

// writesAny grants when the caller may write to any one of the keys.
//
// ANY, not all: a project is gated by two repositories and v1's own
// authorizeProject already grants on either, because the two are a project's
// config and its code and a contributor may hold one of them.
func (s *Service) writesAny(ctx context.Context, token string, keys []string) error {
	var undecided, unconnected error

	for _, spelling := range keys {
		key, ok := parseRepositoryKey(spelling)
		if !ok {
			// A key this cloud stored and this package cannot parse grants
			// nothing. It is skipped rather than treated as an error: one
			// unparseable repository must not make the OTHER one unable to
			// authorize a caller who legitimately holds it.
			s.log("unparseable repository key %q", spelling)
			continue
		}

		forgeToken, err := s.forgeToken(ctx, token, key.Host)
		if err != nil {
			// NOT CONNECTED TO THIS ONE: another key may be on a forge the
			// caller is connected to, and is asked first.
			if nc := notConnected(err); nc != nil {
				unconnected = nc
				continue
			}
			if errors.Is(err, ErrBadCredential) {
				return errUnauthorized
			}
			undecided = errors.Join(undecided, fmt.Errorf("opening the connection to %s: %w", key.Host, err))
			continue
		}

		access, err := s.identity.WriteAccess(ctx, forgeToken, key)
		if err != nil {
			// COULD NOT TELL ABOUT THIS ONE, so keep going. A project is gated
			// by more than one repository, and they need not be on one
			// provider — repokey names its own host precisely so a cloud can
			// hold both — so an adapter that cannot answer for one of them must
			// not shadow the grant the other would have given.
			undecided = errors.Join(undecided, fmt.Errorf("resolving write access to %s: %w", key, err))
			continue
		}

		switch access {
		case AccessGranted:
			return s.admit(ctx, token, forgeToken, key)
		case AccessUnauthenticated:
			// The credential itself is not usable, so the remaining keys would
			// answer the same way. It is a 401 and not a 404 because the caller
			// can fix it.
			return errUnauthorized
		}
	}

	// NOTHING GRANTED. If some repository could not be decided at all, that is
	// the answer — a 502 — and never a denial: a provider outage that silently
	// became "not yours" is a support call rather than a bug report.
	if undecided != nil {
		return undecided
	}
	// A repository the caller might hold on a forge they have not connected
	// is something they can fix, so it is said rather than denied.
	if unconnected != nil {
		return unconnected
	}
	return errDenied
}

// admit asks the cloud whether a caller the forge granted may use it.
//
// IT IS ASKED AFTER THE GRANT, so a caller the forge refuses costs the cloud
// nothing, and a refusal here is a 403 carrying the reason rather than the
// 404 a denial is: the caller holds the repository, and what they lack is
// something they can go and get.
func (s *Service) admit(ctx context.Context, tau, forgeToken string, key repokey.Key) error {
	if err := s.tenancy.Refuse(key); err != nil {
		return problem(http.StatusForbidden, err.Error())
	}
	if s.admissions == nil {
		return nil
	}
	admitted, reason, err := s.admissions.Admit(ctx, Credentials{Forge: forgeToken, Tau: tau}, key)
	switch {
	case err != nil:
		return fmt.Errorf("admitting a caller to %s: %w", key, err)
	case admitted:
		return nil
	case reason == "":
		reason = "not admitted to this cloud"
	}
	return problem(http.StatusForbidden, reason)
}

// parseRepositoryKey accepts only a key already in repokey's canonical form.
//
// The stored value is a KEY ("github.com/acme/pay"), not a clone URL, and
// repokey.Parse requires a scheme — so the check re-derives instead: a key is
// canonical exactly when parsing it as an https URL yields itself back. That
// refuses "GitHub.com/Acme/Pay", a ".git" suffix, a trailing slash, a default
// port, a query and a userinfo, each of which would otherwise be a second
// spelling of one repository.
//
// The backend's jobid.go has the same six lines for the same reason, and they
// are not shared: services/build is the pair's shared vocabulary and its own
// test holds it to three imports, so adding pkg/git/repokey there to save a
// duplicate would widen the surface both roles inherit in order to narrow one
// they do not.
func parseRepositoryKey(key string) (repokey.Key, bool) {
	if key == "" {
		return repokey.Key{}, false
	}
	parsed, err := repokey.Parse("https://" + key)
	if err != nil || parsed.String() != key {
		return repokey.Key{}, false
	}
	return parsed, true
}
