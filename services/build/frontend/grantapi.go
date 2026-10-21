package frontend

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"connectrpc.com/connect"

	"github.com/taubyte/tau/pkg/register"
)

// grantapi.go is an owner sharing a project: roles given to other tau users,
// listed, and revoked. Only an owner reaches any of it.

const maxGrantBody = 4 << 10

type grantRequest struct {
	User string `json:"user"`
	Role string `json:"role"`
}

type grantResponse struct {
	ID   string    `json:"id"`
	User string    `json:"user"`
	Role string    `json:"role"`
	By   string    `json:"by"`
	At   time.Time `json:"at"`
}

func (s *Service) owner(ctx context.Context, token, projectID string) (string, error) {
	if s.linker == nil {
		return "", errDenied
	}
	return s.mayTouchProject(ctx, token, projectID, register.RoleOwner)
}

func (s *Service) handleListGrants(w http.ResponseWriter, r *http.Request, token, projectID string) {
	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.BackendTimeout)
	defer cancel()
	if _, err := s.owner(ctx, token, projectID); err != nil {
		respond(w, err)
		return
	}
	grants, err := s.linker.Grants(ctx, projectID)
	if err != nil {
		respond(w, err)
		return
	}
	out := make([]grantResponse, 0, len(grants))
	for _, g := range grants {
		out = append(out, grantResponse{ID: g.ID, User: g.Grantee, Role: string(g.Role), By: g.By, At: g.At})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Service) handleGrant(w http.ResponseWriter, r *http.Request, token, projectID string) {
	var req grantRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxGrantBody)).Decode(&req); err != nil || req.User == "" {
		respond(w, problem(http.StatusBadRequest, "body must be {user, role}"))
		return
	}
	role, err := register.ParseRole(req.Role)
	if err != nil {
		respond(w, problem(http.StatusBadRequest, err.Error()))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.BackendTimeout)
	defer cancel()
	by, err := s.owner(ctx, token, projectID)
	if err != nil {
		respond(w, err)
		return
	}
	g, err := s.linker.GrantRole(ctx, projectID, req.User, role, by)
	if err != nil {
		respond(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": g.ID})
}

func (s *Service) handleRevoke(w http.ResponseWriter, r *http.Request, token, projectID string) {
	id, err := pathID(r, "grantId")
	if err != nil {
		respond(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.BackendTimeout)
	defer cancel()
	by, err := s.owner(ctx, token, projectID)
	if err != nil {
		respond(w, err)
		return
	}
	err = s.linker.Revoke(ctx, projectID, id, by)
	switch {
	case errors.Is(err, register.ErrNoGrant), connect.CodeOf(err) == connect.CodeNotFound:
		respond(w, errDenied)
	case err != nil:
		respond(w, err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}
