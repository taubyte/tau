package frontend

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
)

// gitapi.go is git's smart HTTP protocol, carried to the forge under the
// caller's connection: /git/<repository key>.git/... is a remote a browser
// clones, fetches and pushes with no forge credential of its own.
//
// IT IS NOT AN OPEN PROXY. The upstream is the forge's own clone address for
// the key, the credential is attached by a transport pinned to that host, and
// before anything is sent the caller is authorized here: read access for
// git-upload-pack, write access for git-receive-pack, and the cloud's tenancy
// and admission for both. The forge then applies its own rules on top.

// maxUploadPackBody bounds a fetch's negotiation: its wants and haves.
const maxUploadPackBody = 8 << 20

const (
	uploadPack  = "git-upload-pack"
	receivePack = "git-receive-pack"
)

// gitRequestHeaders are the headers git's protocol reads; nothing else of
// the caller's request, Authorization least of all, reaches the forge.
var gitRequestHeaders = []string{"Content-Type", "Content-Encoding", "Accept", "Git-Protocol"}

var gitResponseHeaders = []string{"Content-Type", "Cache-Control", "Expires", "Pragma"}

func (s *Service) handleGit(w http.ResponseWriter, r *http.Request, token, path string) {
	if s.repos == nil {
		respond(w, errDenied)
		return
	}
	spelling, op, ok := strings.Cut(path, ".git/")
	if !ok {
		respond(w, errDenied)
		return
	}
	key, ok := parseRepositoryKey(spelling)
	if !ok {
		respond(w, errDenied)
		return
	}
	var service string
	switch {
	case r.Method == http.MethodGet && op == "info/refs":
		service = r.URL.Query().Get("service")
	case r.Method == http.MethodPost && (op == uploadPack || op == receivePack):
		service = op
	default:
		respond(w, errDenied)
		return
	}
	limit := int64(maxUploadPackBody)
	switch service {
	case uploadPack:
	case receivePack:
		limit = s.cfg.MaxPushBytes
	default:
		respond(w, problem(http.StatusBadRequest, "only git's smart protocol is served: service must be git-upload-pack or git-receive-pack"))
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.GitTimeout)
	defer cancel()
	var err error
	if service == receivePack {
		err = s.writesAny(ctx, token, []string{spelling})
	} else {
		err = s.readsKey(ctx, token, key)
	}
	if err != nil {
		respond(w, err)
		return
	}
	forgeToken, err := s.openFor(ctx, token, key.Host)
	if err != nil {
		respond(w, err)
		return
	}
	remote, err := s.repos.Remote(ctx, forgeToken, key)
	if err != nil {
		respond(w, repoProblem(err))
		return
	}

	upstream := *remote.URL
	upstream.Path = strings.TrimSuffix(upstream.Path, "/") + "/" + op
	upstream.RawPath = ""
	upstream.RawQuery = ""
	var body io.Reader = http.NoBody
	if r.Method == http.MethodGet {
		upstream.RawQuery = "service=" + service
	} else {
		body = http.MaxBytesReader(w, r.Body, limit)
	}
	req, err := http.NewRequestWithContext(ctx, r.Method, upstream.String(), body)
	if err != nil {
		respond(w, err)
		return
	}
	if r.Method == http.MethodPost && r.ContentLength > 0 && r.ContentLength <= limit {
		req.ContentLength = r.ContentLength
	}
	for _, h := range gitRequestHeaders {
		if v := r.Header.Get(h); v != "" {
			req.Header.Set(h, v)
		}
	}
	// One round trip and no redirect followed: the transport is pinned to the
	// forge, and a hop it would refuse is a failure rather than a retry.
	resp, err := remote.Transport.RoundTrip(req)
	if err != nil {
		if isTooLarge(err) {
			respond(w, errTooLarge)
			return
		}
		s.log("git %s %s: %v", service, key, err)
		respond(w, errUpstream)
		return
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		s.log("git %s %s: the forge redirected (%d)", service, key, resp.StatusCode)
		respond(w, errUpstream)
		return
	case resp.StatusCode == http.StatusUnauthorized:
		// The forge refused a credential this request has just authorized
		// with. A 401 here would read to a console as its own bearer being
		// bad, and the forge's WWW-Authenticate would raise a browser prompt.
		respond(w, problem(http.StatusForbidden, "the forge refused this connection's credential for "+service+" on "+key.String()))
		return
	}
	for _, h := range gitResponseHeaders {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	if _, err := io.Copy(flushing{w}, resp.Body); err != nil {
		s.log("git %s %s: relaying the answer: %v", service, key, err)
	}
}

// flushing hands each chunk on as it arrives, so a long clone streams rather
// than buffering.
type flushing struct{ w http.ResponseWriter }

func (f flushing) Write(p []byte) (int, error) {
	n, err := f.w.Write(p)
	if err == nil {
		http.NewResponseController(f.w).Flush()
	}
	return n, err
}

func isTooLarge(err error) bool {
	var tooLarge *http.MaxBytesError
	return errors.As(err, &tooLarge)
}
