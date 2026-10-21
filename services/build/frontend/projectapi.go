package frontend

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/taubyte/tau/pkg/register"
)

// projectapi.go is linking a project: the route a developer's CLI calls to make
// a configuration repository and its code repositories one project on this
// cloud, under the id the author's project.yaml carries.
//
// The register owns the project table and answers only over p2p, so a developer
// has no way to reach it; this role is the public one, and the route is the
// thinnest forwarding that keeps the table's rules intact:
//
//   - the caller must hold WRITE access to EVERY repository the link names. One
//     is not enough, because a project made of my config and your code is a
//     way to build your code under my project — and every later job/secret
//     route authorizes on "any one of the project's repositories".
//   - each repository passes the same tenancy and admission check every other
//     route applies (writesAny → admit), so a namespace this cloud does not
//     serve cannot be linked into it.
//   - only then is the claim written, and the register applies its own rules on
//     top: an id another configuration repository owns, or a repository another
//     project owns, is refused by name.
//
// THE CLAIMANT IS THE CALLER, never this node. Who linked a project is the
// forge's answer about the token that was just authorized, which is what makes
// "the caller's projects" a question with an answer.

// maxLinkBody bounds the one body this route reads: a handful of repository
// keys.
const maxLinkBody = 16 << 10

// maxLinkedRepositories bounds how many code repositories one link may attach,
// and so how many forge calls one request can cost.
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
	keys := append([]string{req.Config}, req.Code...)
	for _, key := range keys {
		if _, ok := parseRepositoryKey(key); !ok {
			respond(w, problem(http.StatusBadRequest, "not a repository key: "+key))
			return
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.BackendTimeout)
	defer cancel()

	for _, key := range keys {
		if err := s.writesAny(ctx, token, []string{key}); err != nil {
			respond(w, err)
			return
		}
	}

	config, _ := parseRepositoryKey(req.Config)
	claimant, err := s.claimant(ctx, token, config.Host)
	if err != nil {
		respond(w, err)
		return
	}

	rec, err := s.linker.LinkProject(ctx, register.Link{Project: projectID, Config: req.Config, Code: req.Code, Claimant: claimant})
	switch {
	case errors.Is(err, register.ErrProjectClaimed), errors.Is(err, register.ErrRepositoryClaimed):
		// A CONFLICT THE CALLER CAN ACT ON, said in full. They hold every
		// repository they named, so naming the owner of an id they chose tells
		// them nothing a project.yaml would not.
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

// handleListProjects is GET /forges/{forge}/projects: the projects the token's
// holder, as that forge knows them, linked.
//
// THE FORGE IS NAMED BY THE CALLER, because a token is good on one forge and
// this route must never send it to another to find out which.
func (s *Service) handleListProjects(w http.ResponseWriter, r *http.Request, token, host string) {
	if s.linker == nil {
		respond(w, errDenied)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.BackendTimeout)
	defer cancel()

	claimant, err := s.claimant(ctx, token, host)
	if err != nil {
		respond(w, err)
		return
	}
	recs, err := s.linker.ProjectsOf(ctx, claimant)
	if err != nil {
		s.log("listing %s's projects: %v", claimant, err)
		respond(w, err)
		return
	}
	out := make([]projectResponse, 0, len(recs))
	for _, rec := range recs {
		out = append(out, projectOut(rec))
	}
	writeJSON(w, http.StatusOK, out)
}

// claimant is who the caller is on host, with the forge's refusals turned into
// this route's answers.
func (s *Service) claimant(ctx context.Context, token, host string) (string, error) {
	forgeToken, err := s.forgeToken(ctx, token, host)
	if nc := notConnected(err); nc != nil {
		return "", nc
	}
	if errors.Is(err, ErrBadCredential) {
		return "", errUnauthorized
	}
	if err != nil {
		s.log("opening the connection to %s: %v", host, err)
		return "", errUpstream
	}
	claimant, err := s.identity.Claimant(ctx, forgeToken, host)
	switch {
	case err == nil:
		return claimant, nil
	case errors.Is(err, ErrUnknownForge):
		return "", errDenied
	case errors.Is(err, ErrBadCredential):
		return "", errUnauthorized
	}
	s.log("who holds this token on %s: %v", host, err)
	return "", errUpstream
}

// handleGetProject is GET /projects/{projectId}: one project's repositories,
// to whoever may write to any of them — the same gate every project route
// applies, so a collaborator who did not link it still reads it.
func (s *Service) handleGetProject(w http.ResponseWriter, r *http.Request, token, projectID string) {
	if s.linker == nil {
		respond(w, errDenied)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.BackendTimeout)
	defer cancel()
	if err := s.mayTouchProject(ctx, token, projectID); err != nil {
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
