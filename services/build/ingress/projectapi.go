package ingress

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"connectrpc.com/connect"

	"github.com/taubyte/tau/pkg/register"
)

// projectapi.go links a project: a configuration repository and its code
// repositories, under the id in project.yaml. A link is a registration by its
// owner and no forge is asked: only a forge admin can install the webhook and
// deploy key the cloud needs. Any tau user may link a new id and owns it;
// tenancy applies; the register refuses ids or repositories owned elsewhere; an
// owner relinking replaces the repositories.

// maxLinkBody bounds the one body this route reads: a handful of repository
// keys.
const maxLinkBody = 16 << 10

// maxLinkedRepositories bounds how many code repositories one link may attach.
const maxLinkedRepositories = 32

type linkRequest struct {
	Config string   `json:"config_repository_key"`
	Code   []string `json:"code_repository_keys"`
}

// projectResponse is one linked project on the wire.
type projectResponse struct {
	ID        string    `json:"id"`
	Config    string    `json:"config_repository_key"`
	Code      []string  `json:"code_repository_keys"`
	CreatedAt time.Time `json:"created_at"`
}

func projectOut(rec register.ProjectRecord) projectResponse {
	code := rec.Code
	if code == nil {
		code = []string{}
	}
	return projectResponse{ID: rec.ID, Config: rec.Config, Code: code, CreatedAt: rec.CreatedAt}
}

func (s *Service) handleLinkProject(w http.ResponseWriter, r *http.Request, token, projectID string) {
	if s.linker == nil {
		respond(w, errDenied)
		return
	}

	var req linkRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxLinkBody)).Decode(&req); err != nil {
		respond(w, problem(http.StatusBadRequest, "body must be {config_repository_key, code_repository_keys}"))
		return
	}
	if len(req.Code) > maxLinkedRepositories {
		respond(w, problem(http.StatusBadRequest, "too many code repositories in one link"))
		return
	}
	for _, spelling := range append([]string{req.Config}, req.Code...) {
		key, ok := parseRepositoryKey(spelling)
		if !ok {
			respond(w, problem(http.StatusBadRequest, "not a repository key: "+spelling))
			return
		}
		if err := s.tenancy.Refuse(key); err != nil {
			respond(w, problem(http.StatusForbidden, err.Error()))
			return
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.BackendTimeout)
	defer cancel()

	user, err := s.caller(ctx, token)
	if err != nil {
		respond(w, err)
		return
	}

	replace := false
	role, err := s.projects.Role(ctx, projectID, user)
	switch {
	case err == nil && role.Includes(register.RoleOwner):
		replace = true
	case err == nil:
		respond(w, problem(http.StatusConflict, "project "+projectID+" is already claimed; generate a new id in project.yaml"))
		return
	case errors.Is(err, register.ErrNotLinked), connect.CodeOf(err) == connect.CodeNotFound:
	case errors.Is(err, register.ErrSeparator), errors.Is(err, register.ErrNoProject), connect.CodeOf(err) == connect.CodeInvalidArgument:
		respond(w, problem(http.StatusBadRequest, "not a project id: "+projectID))
		return
	default:
		s.log("linking %s: %v", projectID, err)
		respond(w, err)
		return
	}

	rec, err := s.linker.LinkProject(ctx, register.Link{Project: projectID, Config: req.Config, Code: req.Code, Claimant: user, Replace: replace})
	switch {
	case errors.Is(err, register.ErrProjectClaimed), errors.Is(err, register.ErrRepositoryClaimed):
		respond(w, problem(http.StatusConflict, err.Error()))
		return
	case errors.Is(err, register.ErrSeparator), errors.Is(err, register.ErrNoProject):
		respond(w, problem(http.StatusBadRequest, "not a project id: "+projectID))
		return
	case err != nil:
		s.log("linking %s: %v", projectID, err)
		respond(w, err)
		return
	}
	// The project's repositories just changed, and a cached hook carries them.
	s.secrets.drop(projectID)
	writeJSON(w, http.StatusCreated, projectOut(rec))
}

// handleListProjects is GET /projects: every project the caller owns or holds
// a grant on.
func (s *Service) handleListProjects(w http.ResponseWriter, r *http.Request, token, _ string) {
	if s.linker == nil {
		respond(w, errDenied)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.BackendTimeout)
	defer cancel()

	user, err := s.caller(ctx, token)
	if err != nil {
		respond(w, err)
		return
	}
	recs, err := s.linker.ProjectsOf(ctx, user)
	if err != nil {
		s.log("listing %s's projects: %v", user, err)
		respond(w, err)
		return
	}
	out := make([]projectResponse, 0, len(recs))
	for _, rec := range recs {
		out = append(out, projectOut(rec))
	}
	writeJSON(w, http.StatusOK, out)
}

// handleGetProject is GET /projects/{projectId}: one project's repositories,
// to anyone holding a role on it.
func (s *Service) handleGetProject(w http.ResponseWriter, r *http.Request, token, projectID string) {
	if s.linker == nil {
		respond(w, errDenied)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.BackendTimeout)
	defer cancel()
	if _, err := s.mayTouchProject(ctx, token, projectID, register.RoleViewer); err != nil {
		respond(w, err)
		return
	}
	rec, err := s.linker.ProjectRecord(ctx, projectID)
	if err != nil {
		respond(w, err)
		return
	}
	writeJSON(w, http.StatusOK, projectOut(rec))
}
