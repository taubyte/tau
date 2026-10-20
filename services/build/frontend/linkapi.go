package frontend

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/taubyte/tau/services/build"
)

// linkapi.go is what `tau link` has done at the forge: reading a repository
// before the claim, and installing the project's webhook and deploy keys
// after it.
//
// THE CLIENT NEVER HOLDS A FORGE CREDENTIAL. The caller's connection is
// opened here, for the repository's host, used for these calls and dropped
// with the request — the same way every other route uses it — so a stolen tau
// token is never turned into direct, lasting forge access. The cloud still
// stores no forge credential: opening one in a request is all it does.
//
// EACH ROUTE IS AS NARROW AS ITS ONE JOB:
//
//   - both authorize as every route does, write access to the repository
//     through the caller's connection, and its tenancy and admission
//     (writesAny);
//   - install reaches only a repository the project is linked to, delivers
//     only to this cloud's own /hook/<id>, and installs only the project's
//     STORED hook secret, which the caller does not send; what it is given is
//     a deploy key's public half;
//   - a hook already there is replaced only when the caller says so.

const maxLinkInstallBody = 8 << 10

type inspectRequest struct {
	Repository string `json:"repository"`
}

type inspectResponse struct {
	Repository string `json:"repository"`
	Admin      bool   `json:"admin"`
	Hooked     bool   `json:"hooked"`
	Callback   string `json:"callback"`
}

type installRequest struct {
	Repository string `json:"repository"`
	DeployKey  string `json:"deploy_key"`
	Replace    bool   `json:"replace"`
}

// callback is where a hook for projectID delivers, and the cloud a deploy
// key is titled for. IT IS NEVER THE REQUEST'S: the Host header and TLS state
// are the caller's to set, and a hook aimed by them would deliver this
// project's pushes, signed with its secret, wherever the caller named.
//
// It is the cloud's own name for this service, `https://<name>.<cloud fqdn>`,
// which every frontend of a cloud derives alike and the cloud's dns answers.
// A configured PublicURL overrides it, for a frontend an operator serves
// behind a proxy or a domain of their own.
func (s *Service) callback(projectID string) (*url.URL, string, error) {
	base := strings.TrimSuffix(s.cfg.PublicURL, "/")
	if base == "" && s.cloudFqdn != "" {
		base = "https://" + build.Frontend.Name + "." + s.cloudFqdn
	}
	if base == "" {
		return nil, "", problem(http.StatusServiceUnavailable, "this cloud's build frontend has no cloud fqdn to be named under and no publicURL configured, so it cannot install a webhook; link with --manual or ask the operator to set the gateway's cloudFqdn (or the frontend's publicURL)")
	}
	u, err := url.Parse(base + "/hook/" + url.PathEscape(projectID))
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, "", problem(http.StatusInternalServerError, "this cloud's publicURL is not an http(s) address to deliver webhooks to")
	}
	return u, base, nil
}

func (s *Service) handleLinkInspect(w http.ResponseWriter, r *http.Request, token, projectID string) {
	if s.installer == nil {
		respond(w, errDenied)
		return
	}
	var req inspectRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxLinkInstallBody)).Decode(&req); err != nil {
		respond(w, problem(http.StatusBadRequest, "body must be {repository}"))
		return
	}
	key, ok := parseRepositoryKey(req.Repository)
	if !ok {
		respond(w, problem(http.StatusBadRequest, "not a repository key: "+req.Repository))
		return
	}
	callback, _, err := s.callback(projectID)
	if err != nil {
		respond(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.BackendTimeout)
	defer cancel()
	if err := s.writesAny(ctx, token, []string{req.Repository}); err != nil {
		respond(w, err)
		return
	}
	forgeToken, err := s.forgeToken(ctx, token, key.Host)
	if err != nil {
		respond(w, err)
		return
	}
	got, err := s.installer.Inspect(ctx, forgeToken, key, callback)
	if err != nil {
		respond(w, installProblem(err))
		return
	}
	writeJSON(w, http.StatusOK, inspectResponse{Repository: got.Repository, Admin: got.Admin, Hooked: got.Hooked, Callback: callback.String()})
}

func (s *Service) handleLinkInstall(w http.ResponseWriter, r *http.Request, token, projectID string) {
	if s.installer == nil {
		respond(w, errDenied)
		return
	}
	var req installRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxLinkInstallBody)).Decode(&req); err != nil {
		respond(w, problem(http.StatusBadRequest, "body must be {repository, deploy_key, replace}"))
		return
	}
	key, ok := parseRepositoryKey(req.Repository)
	if !ok {
		respond(w, problem(http.StatusBadRequest, "not a repository key: "+req.Repository))
		return
	}
	if !strings.HasPrefix(req.DeployKey, "ssh-") || strings.ContainsAny(req.DeployKey, "\r\n") {
		respond(w, problem(http.StatusBadRequest, "deploy_key must be one authorized_keys line"))
		return
	}
	callback, base, err := s.callback(projectID)
	if err != nil {
		respond(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.BackendTimeout)
	defer cancel()
	if err := s.writesAny(ctx, token, []string{req.Repository}); err != nil {
		respond(w, err)
		return
	}

	// THE PROJECT'S OWN RECORD, read fresh rather than through the webhook
	// cache: the link that called this has just stored a new secret.
	hook, err := s.hooks.Hook(ctx, projectID)
	switch {
	case errors.Is(err, ErrNoHook):
		respond(w, problem(http.StatusConflict, "the project has no stored hook secret yet: link it first"))
		return
	case err != nil:
		respond(w, err)
		return
	case !slices.Contains(hook.Repositories, req.Repository):
		respond(w, problem(http.StatusBadRequest, req.Repository+" is not one of the project's repositories"))
		return
	}

	forgeToken, err := s.forgeToken(ctx, token, key.Host)
	if err != nil {
		respond(w, err)
		return
	}
	err = s.installer.Install(ctx, forgeToken, key, Install{
		Callback: callback, Secret: hook.Secret, Title: "tau " + base, DeployKey: req.DeployKey, Replace: req.Replace,
	})
	if err != nil {
		respond(w, installProblem(err))
		return
	}
	// A hook verified with the secret just installed must not meet an old one
	// remembered here.
	s.secrets.drop(projectID)
	w.WriteHeader(http.StatusNoContent)
}

// installProblem says the refusals a caller can act on, and leaves the rest
// to respond.
func installProblem(err error) error {
	for _, e := range []struct {
		err    error
		status int
	}{
		{ErrNotAdmin, http.StatusForbidden},
		{ErrAlreadyHooked, http.StatusConflict},
		{ErrCannotInstall, http.StatusUnprocessableEntity},
	} {
		if errors.Is(err, e.err) {
			return problem(e.status, err.Error())
		}
	}
	return err
}
