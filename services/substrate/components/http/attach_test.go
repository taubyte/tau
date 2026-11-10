package http

import (
	"errors"
	"net"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	nodeIface "github.com/taubyte/tau/core/services/substrate"
	"github.com/taubyte/tau/p2p/peer"
	httptun "github.com/taubyte/tau/p2p/streams/tunnels/http"
	"github.com/taubyte/tau/pkg/config"
	http "github.com/taubyte/tau/pkg/http"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
)

type countingHttp struct {
	http.Service
	catchAlls int
}

func (c *countingHttp) LowLevel(*http.LowLevelDefinition) *mux.Route {
	c.catchAlls++
	return nil
}

type nodeWithHttp struct {
	nodeIface.Service
	http *countingHttp
}

func (n nodeWithHttp) Http() http.Service { return n.http }

type shapeConfig struct {
	config.Config
	services []string
}

func (c shapeConfig) Services() []string { return c.services }

// Two catch-alls on one router: the first registered gets every request.
func TestCatchAllLeftToColocatedGateway(t *testing.T) {
	for services, want := range map[*[]string]int{
		{"auth", "substrate", "patrick"}: 1,
		{"auth", "substrate", "gateway"}: 0,
		{"substrate"}:                    1,
	} {
		h := new(countingHttp)
		s := &Service{Service: nodeWithHttp{http: h}, config: shapeConfig{services: *services}}
		if err := s.attach(); err != nil {
			t.Fatal(err)
		}
		if h.catchAlls != want {
			t.Errorf("%v: %d catch-alls, want %d", *services, h.catchAlls, want)
		}
	}
}

// A real admission refusal, over a real tunnel, must reach the frontend as a
// refusal it can act on, with nothing written to the client.
func TestAdmissionRefusalReachesFrontend(t *testing.T) {
	s := newTestService(peer.Mock(t.Context()))
	domains = map[string]structureSpec.Domain{"id": {Name: "d", Fqdn: successDomain}}
	functions = map[string]structureSpec.Function{"f": {Id: "f", Name: "f", Domains: []string{"d"}, Paths: []string{"/ping"}, Method: "GET"}}
	websites = map[string]structureSpec.Website{}
	if err := fakeFetch(s.Tns(), websites, functions, domains); err != nil {
		t.Fatal(err)
	}
	s.inflight = newLimiter(1, 1, 1)
	<-s.inflight.tokens
	s.inflight.waiting.Store(1) // the queue is full too: refused at once

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go func() {
		c, err := l.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		w, r, err := httptun.Backend(c)
		if err != nil {
			return
		}
		s.Handler(w, r)
	}()
	c, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	rec := httptest.NewRecorder()
	err = httptun.Frontend(rec, httptest.NewRequest("GET", "http://"+successDomain+"/ping", nil), c)
	if !errors.Is(err, httptun.ErrRefused) {
		t.Fatalf("err = %v, status %d body %q", err, rec.Code, rec.Body)
	}
}
