package frontend

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"connectrpc.com/connect"

	"github.com/taubyte/tau/pkg/git/repokey"
	buildv2 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v2"
	"github.com/taubyte/tau/pkg/register"
)

// authz.go is the only file in this package that decides whether a caller may
// do a thing, and it decides it ON THE PROJECT: the caller is the tau user
// auth verifies their token as, and what they may do is their role in the
// register's project table. No forge is asked anything.
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
	return boundedPath(r, name, maxIDLength)
}

func boundedPath(r *http.Request, name string, bound int) (string, error) {
	id := r.PathValue(name)
	if id == "" || len(id) > bound {
		return "", errDenied
	}
	return id, nil
}

// caller is the tau user the token was issued to.
func (s *Service) caller(ctx context.Context, token string) (string, error) {
	user, err := s.callers.Caller(ctx, token)
	switch {
	case errors.Is(err, ErrBadCredential), connect.CodeOf(err) == connect.CodeUnauthenticated:
		return "", errUnauthorized
	case err != nil:
		s.log("verifying a caller: %v", err)
		return "", errUpstream
	case user == "":
		return "", errUnauthorized
	}
	return user, nil
}

// mayTouchProject authorizes a project-scoped route for a caller holding at
// least want, and hands back who they are.
func (s *Service) mayTouchProject(ctx context.Context, token, projectID string, want register.Role) (string, error) {
	user, err := s.caller(ctx, token)
	if err != nil {
		return "", err
	}
	role, err := s.projects.Role(ctx, projectID, user)
	if err != nil {
		return "", err
	}
	if !role.Includes(want) {
		return "", errDenied
	}
	return user, nil
}

// mayTouchJob authorizes a job-scoped route on the project the job's
// repository belongs to, and hands back the job it had to read to do it.
func (s *Service) mayTouchJob(ctx context.Context, token, jobID string, want register.Role) (*buildv2.Job, error) {
	user, err := s.caller(ctx, token)
	if err != nil {
		return nil, err
	}
	job, err := s.backend.Get(ctx, jobID)
	if err != nil {
		return nil, err
	}
	projectID, err := s.projects.Project(ctx, job.GetRepositoryKey())
	if err != nil {
		return nil, err
	}
	role, err := s.projects.Role(ctx, projectID, user)
	if err != nil {
		return nil, err
	}
	if !role.Includes(want) {
		return nil, errDenied
	}
	return job, nil
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
