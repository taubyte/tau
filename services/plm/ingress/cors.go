package ingress

import (
	"net/http"
	"slices"
	"strings"
)

// cors.go lets the cloud's own consoles call this ingress from a browser. Origins
// match exactly; credentials travel only as the Authorization header, never
// cookies, so Allow-Credentials is never sent.

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
