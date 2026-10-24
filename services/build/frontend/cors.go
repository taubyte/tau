package frontend

import (
	"net/http"
	"slices"
	"strings"
)

// cors.go lets the cloud's own consoles call this ingress from a browser.
//
// AN ORIGIN IS ALLOWED BY EXACT MATCH against Config.Origins, and nothing
// else is: no wildcard, no reflection of whatever Origin arrived. Credentials
// travel as a header a page sets on purpose — Authorization, the tau token —
// never as cookies, so Access-Control-Allow-Credentials is never sent and a
// page on another origin cannot ride a signed-in browser.

var (
	corsHeaders = strings.Join([]string{"Authorization", "Content-Type", "Connect-Protocol-Version", "Connect-Timeout-Ms", "Git-Protocol"}, ", ")
	corsMethods = "GET, POST, PUT, DELETE"
)

// corsHandler is a value, not a closure, so a Handler is comparable.
type corsHandler struct {
	s    *Service
	next http.Handler
}

func (s *Service) cors(next http.Handler) http.Handler { return &corsHandler{s: s, next: next} }

func (c *corsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s, next := c.s, c.next
	{
		origin := r.Header.Get("Origin")
		if origin == "" || !slices.Contains(s.cfg.Origins, origin) {
			next.ServeHTTP(w, r)
			return
		}
		h := w.Header()
		h.Add("Vary", "Origin")
		h.Set("Access-Control-Allow-Origin", origin)
		h.Set("Access-Control-Expose-Headers", "Content-Type, "+attemptHeader)
		if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
			h.Set("Access-Control-Allow-Methods", corsMethods)
			h.Set("Access-Control-Allow-Headers", corsHeaders)
			h.Set("Access-Control-Max-Age", "600")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	}
}
