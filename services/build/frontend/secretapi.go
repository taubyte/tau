package frontend

import (
	"encoding/json"
	"io"
	"net/http"
)

// secretapi.go is where a developer's secret values are written, and it is the
// one write in v2 that is NOT a git push.
//
// EVERYTHING ELSE THIS CLOUD RUNS COMES FROM A COMMIT. The register's only
// writer is the build service, because a deploy from a laptop bypasses git, its
// authorization and its record. A secret inverts that exactly: putting the
// value in git IS the failure, so it is written out of band and a rotation is
// one call with no commit and no build behind it. That is the vocabulary's own
// statement about the `secret` kind, not a convenience added here.
//
// SO THE AUTHORIZATION HAS TO BE THE SAME. What git gave the other writes is
// "may this person change what this project runs", and here that is
// mayTouchProject: write access to one of the repositories the project is made
// of, the identical rule the deploy path applies. A secret is at least as
// powerful as a commit, so it may not be easier to obtain.
//
// THERE IS NO READ ROUTE, and there will not be one. See Vault in config.go: a
// developer sets a value and lists which names are set. A GET of a value would
// turn a leaked developer token from "they can overwrite my secrets" into "they
// have every secret this project holds", and it buys nothing — the value was
// theirs when they set it, and rotating is setting again.

// maxSecretValue bounds the body of a Set before it is read.
//
// A SECRET IS A CREDENTIAL, NOT A FILE. The largest thing anybody legitimately
// puts here is a certificate chain with a private key; 64KiB is several of
// those. Without a cap this is an authenticated route that will hold whatever a
// caller sends in memory and then replicate it to every node in the swarm.
const maxSecretValue = 64 << 10

// handleListSecrets is GET /secrets/{projectId}/{environment}.
//
// NAMES, NEVER VALUES. This answers "is DATABASE_URL set in production", which
// is what a developer checks before a deploy and what a CLI shows.
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

// handleSetSecret is PUT /secrets/{projectId}/{environment}/{name}.
//
// THE BODY IS THE VALUE, raw. It is not a JSON field, because wrapping it would
// mean a value containing a quote or a newline depends on the caller escaping
// it correctly — and a secret is exactly the string nobody looks at again.
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

	// BOUNDED BEFORE IT IS READ. MaxBytesReader makes the read itself fail at
	// the limit, so an oversized body is never held; reading first and checking
	// the length after is the version of this that does not work.
	value, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxSecretValue))
	if err != nil {
		respond(w, errTooLarge)
		return
	}

	if err := s.vault.Set(r.Context(), projectID, environment, name, value); err != nil {
		respond(w, err)
		return
	}
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

// mayTouchSecrets is mayTouchProject, plus the one thing that is different
// here: a frontend with no vault refuses exactly as a denial does.
//
// THE ORDER MATTERS AND IT IS THIS WAY ROUND ON PURPOSE. The vault check is
// first because it costs nothing and asks nothing of auth — and because
// answering it after authorization would make these routes tell a caller who
// may touch the project whether this cloud has a vault, which is a fact about
// the deployment rather than about them.
func (s *Service) mayTouchSecrets(r *http.Request, token, projectID string) error {
	if s.vault == nil {
		return errDenied
	}
	return s.mayTouchProject(r.Context(), token, projectID)
}
