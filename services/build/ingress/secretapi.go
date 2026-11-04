package ingress

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/taubyte/tau/pkg/register"
)

// secretapi.go writes a developer's secret values: the one write not from a git
// push, since the value in git is the failure. It needs the secret-manager role.
// There is no read route: a leaked token can overwrite secrets, never read them.

// maxSecretValue bounds a Set body before it is read: a secret is a credential,
// and a certificate chain with key fits several times over.
const maxSecretValue = 64 << 10

// handleListSecrets is GET /secrets/{projectId}/{environment}: names, never
// values.
func (s *Service) handleListSecrets(w http.ResponseWriter, r *http.Request, token, projectID string) {
	environment, err := pathID(r, "environment")
	if err != nil {
		respond(w, err)
		return
	}
	if err := s.mayTouchSecrets(r, token, projectID); err != nil {
		respond(w, err)
		return
	}

	names, err := s.vault.List(r.Context(), projectID, environment)
	if err != nil {
		respond(w, err)
		return
	}

	// An environment with nothing set is an empty list and not a 404: the
	// question has an answer and the answer is none.
	if names == nil {
		names = []string{}
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(names); err != nil {
		s.log("writing secret names for %s/%s: %v", projectID, environment, err)
	}
}

// handleSetSecret is PUT /secrets/{projectId}/{environment}/{name}; the raw body
// is the value, so no escaping is involved.
func (s *Service) handleSetSecret(w http.ResponseWriter, r *http.Request, token, projectID string) {
	environment, name, err := s.secretPath(r)
	if err != nil {
		respond(w, err)
		return
	}
	if err := s.mayTouchSecrets(r, token, projectID); err != nil {
		respond(w, err)
		return
	}

	// Bounded before it is read: MaxBytesReader fails the read at the limit.
	value, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxSecretValue))
	if err != nil {
		respond(w, errTooLarge)
		return
	}

	if err := s.vault.Set(r.Context(), projectID, environment, name, value); err != nil {
		respond(w, err)
		return
	}
	// A rotated hook secret is believed now on this frontend; other frontends catch up
	// by re-asking on a failed verification (webhook.go).
	s.secrets.drop(projectID)
	w.WriteHeader(http.StatusNoContent)
}

// handleDeleteSecret is DELETE /secrets/{projectId}/{environment}/{name}.
// Deleting what is not there succeeds: the caller wanted it gone and it is
// gone.
func (s *Service) handleDeleteSecret(w http.ResponseWriter, r *http.Request, token, projectID string) {
	environment, name, err := s.secretPath(r)
	if err != nil {
		respond(w, err)
		return
	}
	if err := s.mayTouchSecrets(r, token, projectID); err != nil {
		respond(w, err)
		return
	}

	if err := s.vault.Delete(r.Context(), projectID, environment, name); err != nil {
		respond(w, err)
		return
	}
	s.secrets.drop(projectID)
	w.WriteHeader(http.StatusNoContent)
}

// secretPath reads the two bounded segments beyond the project id. admitted()
// has already read and bounded that one.
func (s *Service) secretPath(r *http.Request) (environment, name string, err error) {
	if environment, err = pathID(r, "environment"); err != nil {
		return "", "", err
	}
	if name, err = pathID(r, "name"); err != nil {
		return "", "", err
	}
	return environment, name, nil
}

// mayTouchSecrets is mayTouchProject, refusing first (as a denial) when there is no
// vault, so the answer reveals nothing about the deployment.
func (s *Service) mayTouchSecrets(r *http.Request, token, projectID string) error {
	if s.vault == nil {
		return errDenied
	}
	_, err := s.mayTouchProject(r.Context(), token, projectID, register.RoleSecretManager)
	return err
}
