package frontend

import (
	"context"
	"encoding/json"
	"net/http"
)

// projectapi.go is linking a project: the route a developer's CLI calls to make
// a configuration repository and a code repository one project on this cloud.
//
// auth owns projects and repositories and answers only over p2p, so a developer
// has no way to reach it; this role is the public one, and the route is the
// thinnest forwarding that keeps auth's rules intact:
//
//   - the caller must hold WRITE access to BOTH repositories. One is not enough,
//     because a project made of my config and your code is a way to build your
//     code under my project — and every later job/secret route authorizes on
//     "any one of the project's repositories".
//   - each repository passes the same tenancy and admission check every other
//     route applies (writesAny → admit), so a namespace this cloud does not
//     serve cannot be linked into it.
//   - only then are the repositories registered and the project created, and
//     auth applies its own checks (tenancy again, registrant ownership) on top.
//
// The registrant auth records is this frontend's peer, not the developer: auth
// has no way to read a forge token. That is the same trust auth already extends
// to any swarm peer, and it is why the forge check above must come first.

// maxLinkBody bounds the one body this route reads: two repository keys.
const maxLinkBody = 4 << 10

type linkRequest struct {
	Config string `json:"config_repository_key"`
	Code   string `json:"code_repository_key"`
}

type linkResponse struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (s *Service) handleCreateProject(w http.ResponseWriter, r *http.Request, token, name string) {
	if s.registrar == nil {
		respond(w, errDenied)
		return
	}

	var req linkRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxLinkBody)).Decode(&req); err != nil {
		respond(w, problem(http.StatusBadRequest, "body must be {config_repository_key, code_repository_key}"))
		return
	}
	keys := []string{req.Config, req.Code}
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
	for _, key := range keys {
		if err := s.registrar.Register(ctx, key); err != nil {
			s.log("registering %s: %v", key, err)
			respond(w, err)
			return
		}
	}
	id, err := s.registrar.Create(ctx, name, req.Config, req.Code)
	if err != nil {
		s.log("creating project %q: %v", name, err)
		respond(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, linkResponse{ID: id, Name: name})
}
