package frontend

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/taubyte/tau/pkg/git/repokey"
)

// repoapi.go is a console's repository picker and its "new project": the
// caller's repositories on one forge, and a new one made there. Both are asked
// with the caller's own connection, opened here and dropped with the request,
// like every other route.

const maxCreateBody = 8 << 10

// maxListedRepositories bounds one listing, and so how many pages one request
// asks a forge for.
const maxListedRepositories = 1000

type repositoriesResponse struct {
	Repositories []RepositoryInfo `json:"repositories"`
}

type createRequest struct {
	Repository  string `json:"repository"`
	Private     bool   `json:"private"`
	Description string `json:"description"`
}

// openFor is the caller's forge credential for host, with every way opening
// it can fail already said as an answer.
func (s *Service) openFor(ctx context.Context, token, host string) (string, error) {
	forgeToken, err := s.forgeToken(ctx, token, host)
	switch {
	case err == nil:
		return forgeToken, nil
	case notConnected(err) != nil:
		return "", notConnected(err)
	case errors.Is(err, ErrBadCredential):
		return "", errUnauthorized
	}
	return "", err
}

// repoProblem says the refusals a caller can act on, and leaves the rest to
// respond.
func repoProblem(err error) error {
	switch {
	case errors.Is(err, ErrUnsupported):
		return problem(http.StatusNotImplemented, err.Error())
	case errors.Is(err, ErrExists):
		return problem(http.StatusConflict, err.Error())
	case errors.Is(err, ErrUnknownForge):
		return problem(http.StatusNotFound, err.Error())
	case errors.Is(err, ErrBadCredential):
		return errUnauthorized
	}
	return err
}

// readsKey grants when the caller may read key, and the cloud admits them.
// Read, not write: it gates only what the forge would hand the same
// credential anyway, a clone. Every route that changes a repository asks
// writesAny instead.
func (s *Service) readsKey(ctx context.Context, token string, key repokey.Key) error {
	forgeToken, err := s.openFor(ctx, token, key.Host)
	if err != nil {
		return err
	}
	access, err := s.repos.ReadAccess(ctx, forgeToken, key)
	switch {
	case err != nil:
		return repoProblem(err)
	case access == AccessUnauthenticated:
		return errUnauthorized
	case access != AccessGranted:
		return errDenied
	}
	return s.admit(ctx, token, forgeToken, key)
}

// handleListRepositories is GET /forges/{forge}/repositories: what the
// caller reaches there, less what this cloud's tenancy does not serve.
func (s *Service) handleListRepositories(w http.ResponseWriter, r *http.Request, token, host string) {
	if s.repos == nil {
		respond(w, errDenied)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.BackendTimeout)
	defer cancel()
	forgeToken, err := s.openFor(ctx, token, host)
	if err != nil {
		respond(w, err)
		return
	}
	listed, err := s.repos.List(ctx, forgeToken, host)
	if err != nil {
		respond(w, repoProblem(err))
		return
	}
	out := []RepositoryInfo{}
	var first *repokey.Key
	for _, repo := range listed {
		key, ok := parseRepositoryKey(repo.Repository)
		if !ok || key.Host != host || s.tenancy.Refuse(key) != nil {
			continue
		}
		if first == nil {
			first = &key
		}
		out = append(out, repo)
		if len(out) == maxListedRepositories {
			break
		}
	}
	// ADMISSION IS ABOUT THE PERSON, and the forge account is the same for
	// every repository listed, so asking once is asking for all of them.
	if first != nil {
		if err := s.admit(ctx, token, forgeToken, *first); err != nil {
			respond(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, repositoriesResponse{Repositories: out})
}

// handleCreateRepository is POST /forges/{forge}/repositories: a new, empty
// repository at the key the caller names, made with their own credential, so
// the forge decides whether they may create in that namespace.
func (s *Service) handleCreateRepository(w http.ResponseWriter, r *http.Request, token, host string) {
	if s.repos == nil {
		respond(w, errDenied)
		return
	}
	var req createRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxCreateBody)).Decode(&req); err != nil {
		respond(w, problem(http.StatusBadRequest, "body must be {repository, private, description}"))
		return
	}
	key, ok := parseRepositoryKey(req.Repository)
	if !ok {
		respond(w, problem(http.StatusBadRequest, "not a repository key: "+req.Repository))
		return
	}
	if key.Host != host {
		respond(w, problem(http.StatusBadRequest, req.Repository+" is not on "+host))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.BackendTimeout)
	defer cancel()
	forgeToken, err := s.openFor(ctx, token, host)
	if err != nil {
		respond(w, err)
		return
	}
	// THE CLOUD'S ANSWER COMES FIRST: a namespace it does not serve, or a
	// caller it does not admit, gets no repository made on its behalf.
	if err := s.admit(ctx, token, forgeToken, key); err != nil {
		respond(w, err)
		return
	}
	made, err := s.repos.Create(ctx, forgeToken, key, NewRepository{Description: req.Description, Private: req.Private})
	if err != nil {
		respond(w, repoProblem(err))
		return
	}
	writeJSON(w, http.StatusCreated, made)
}
