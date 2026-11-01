package frontend

import (
	"context"
	"io"
	"net/http"

	"github.com/taubyte/tau/pkg/register"
)

// assetapi.go reads back what a finished build left: a log by its record key and
// an artifact by resource id, authorized as the job is and looked up in its own
// maps. No caller ever names a cid.

// handleJobLog is GET /job/{jid}/logs/{key}: one finished log.
func (s *Service) handleJobLog(w http.ResponseWriter, r *http.Request, token, jobID string) {
	s.serveJobAsset(w, r, token, jobID, "key", func(_, logs map[string]string, key string) string { return logs[key] }, "text/plain; charset=utf-8")
}

// handleJobAsset is GET /job/{jid}/assets/{resource}: one built artifact.
func (s *Service) handleJobAsset(w http.ResponseWriter, r *http.Request, token, jobID string) {
	s.serveJobAsset(w, r, token, jobID, "resource", func(assets, _ map[string]string, key string) string { return assets[key] }, "application/octet-stream")
}

func (s *Service) serveJobAsset(w http.ResponseWriter, r *http.Request, token, jobID, name string, pick func(assets, logs map[string]string, key string) string, contentType string) {
	if s.assets == nil {
		respond(w, problem(http.StatusServiceUnavailable, "this node reads no asset store"))
		return
	}
	key, err := pathID(r, name)
	if err != nil {
		respond(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.BackendTimeout)
	defer cancel()
	job, err := s.mayTouchJob(ctx, token, jobID, register.RoleViewer)
	if err != nil {
		respond(w, err)
		return
	}
	cid := pick(job.GetAssetCid(), job.GetLogs(), key)
	if cid == "" {
		respond(w, errDenied)
		return
	}
	// The read outlives BackendTimeout: an artifact is as large as its build
	// made it, and the transfer is bounded by the caller hanging up.
	content, err := s.assets.Get(r.Context(), cid)
	if err != nil {
		s.log("reading %s of job %s: %v", cid, jobID, err)
		respond(w, errUpstream)
		return
	}
	defer content.Close()
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if _, err := io.Copy(w, content); err != nil {
		s.log("relaying %s of job %s: %v", cid, jobID, err)
	}
}

type cloudResponse struct {
	Fqdn string `json:"fqdn"`
	// Uploads is whether this is a development cloud that takes a developer's
	// build straight in (buildapi.go).
	Uploads bool `json:"uploads,omitempty"`
}

// handleCloud is GET /cloud: the name this cloud serves under, which every
// generated name ends in. A bearer is asked for, as everywhere else: what
// this process tells a stranger about itself is nothing.
func (s *Service) handleCloud(w http.ResponseWriter, r *http.Request) {
	token, err := bearer(r)
	if err != nil {
		respond(w, err)
		return
	}
	if !s.globalJobs.allow() || !s.allowCaller(token) {
		respond(w, tooManyRequests(s.cfg.CallerRate.Per))
		return
	}
	if s.cloudFqdn == "" {
		respond(w, problem(http.StatusServiceUnavailable, "this build frontend was given no cloud fqdn"))
		return
	}
	writeJSON(w, http.StatusOK, cloudResponse{Fqdn: s.cloudFqdn, Uploads: s.uploads != nil})
}
