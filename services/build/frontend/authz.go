package frontend

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/taubyte/tau/pkg/git/repokey"
	buildv1 "github.com/taubyte/tau/pkg/v2/proto/gen/tau/build/v1"
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
// The subject is the project, and what gates it is auth's own answer about
// which repositories the project is made of — the same scoping
// services/auth/github.go's authorizeProject applies when it lists projects, so
// a project is reachable by id to exactly the people who already see it in
// their own list.
func (s *Service) mayTouchProject(ctx context.Context, token, projectID string) error {
	keys, err := s.projects.Repositories(ctx, projectID)
	if err != nil {
		// An unknown project and an unreachable auth are NOT the same answer,
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
func (s *Service) mayTouchJob(ctx context.Context, token, jobID string) (*buildv1.Job, error) {
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
	var undecided error

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

		access, err := s.identity.WriteAccess(ctx, token, key)
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
			return nil
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
	return errDenied
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
