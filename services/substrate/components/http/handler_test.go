package http

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/taubyte/tau/p2p/peer"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
)

// Nothing is deployed at the path: 404, and the body names neither host, path nor project.
func TestHandlerLookupMissIs404(t *testing.T) {
	s := newTestService(peer.Mock(t.Context()))
	domains = map[string]structureSpec.Domain{"d": {Name: "dom", Fqdn: successDomain}}
	functions = map[string]structureSpec.Function{"f": {Id: "f", Name: "f", Domains: []string{"dom"}, Paths: []string{"/ping"}, Method: "GET"}}
	websites = map[string]structureSpec.Website{}
	if err := fakeFetch(s.Tns(), websites, functions, domains); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	s.Handler(w, httptest.NewRequest("GET", "http://"+successDomain+"/secret-path", nil))
	if w.Code != 404 {
		t.Fatalf("status %d body %s", w.Code, w.Body)
	}
	for _, s := range []string{successDomain, "secret-path", testProject} {
		if strings.Contains(w.Body.String(), s) {
			t.Fatalf("body leaks %q: %s", s, w.Body)
		}
	}
}
