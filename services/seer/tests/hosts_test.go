//go:build dreaming

package tests

import (
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"testing"
	"time"

	seerClient "github.com/taubyte/tau/clients/p2p/seer"
	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	dreamCommon "github.com/taubyte/tau/dream/common"
	"github.com/taubyte/tau/p2p/peer"
	tauConfig "github.com/taubyte/tau/pkg/config"
	tauhttp "github.com/taubyte/tau/pkg/http"
	httpauth "github.com/taubyte/tau/pkg/http/auth"
	commonSpecs "github.com/taubyte/tau/pkg/specs/common"
	"github.com/taubyte/tau/services/common/httpsvc"

	dns "github.com/miekg/dns"
	"gotest.tools/v3/assert"

	_ "github.com/taubyte/tau/services/seer/dream"
)

// mockService is a bare service registered through the same seam ee services use
// (RegisterService + Registry.Set): a node that beacons under its name so seer
// can resolve it, plus its own HTTP server (one per node — Dream doesn't share a
// shape's http). Used to test domains.hosts routing without a real service.
type mockService struct {
	node peer.Node
	http tauhttp.Service // its own HTTP listener (nil if creation failed)
}

func (m *mockService) Node() peer.Node { return m.node }
func (m *mockService) Close() error {
	if m.http != nil {
		m.http.Stop()
	}
	return nil // universe owns the node
}

func init() {
	commonSpecs.RegisterService("mockhost", commonSpecs.ServiceCapabilities{HTTP: true})
	if err := dream.Registry.Set("mockhost", createMockHost, nil); err != nil {
		panic(err)
	}
}

func createMockHost(u *dream.Universe, config *commonIface.ServiceConfig) (commonIface.Service, error) {
	cfg, err := dreamCommon.NewConfig(u, config)
	if err != nil {
		return nil, err
	}
	node, err := tauConfig.NewNode(u.Context(), cfg, path.Join(cfg.Root(), "mockhost"))
	if err != nil {
		return nil, err
	}
	if err := dreamCommon.StartBeacon(u.Context(), cfg, node, "mockhost"); err != nil {
		return nil, err
	}

	// Own HTTP server, with /whoami scoped to all custom domains bound to
	// mockhost (domains.hosts) in one registration via Hosts. With no binding
	// (the DNS-only test) no route is registered, so the server just answers 404.
	h, err := httpsvc.New(u.Context(), node, cfg)
	if err != nil {
		return nil, err
	}
	if hosts := cfg.HostsForService("mockhost"); len(hosts) > 0 {
		h.GET(&tauhttp.RouteDefinition{
			Hosts: hosts,
			Path:  "/whoami",
			Auth:  tauhttp.RouteAuthHandler{Validator: httpauth.AnonymousHandler},
			Handler: func(tauhttp.Context) (any, error) {
				return "mockhost", nil
			},
		})
	}
	h.Start()

	return &mockService{node: node, http: h}, nil
}

// TestDns_HostBinding_Dreaming binds a custom domain (admin.<fqdn>) to the
// mockhost service via domains.hosts and asserts seer resolves that domain to
// mockhost's node — the same node its direct <svc>.tau.<fqdn> name resolves to.
func TestDns_HostBinding_Dreaming(t *testing.T) {
	seerClient.DefaultUsageBeaconInterval = 100 * time.Millisecond
	seerClient.DefaultAnnounceBeaconInterval = 100 * time.Millisecond

	m, err := dream.New(t.Context())
	assert.NilError(t, err)
	defer m.Close()

	u, err := m.New(dream.UniverseConfig{Name: t.Name()})
	assert.NilError(t, err)

	fqdn := strings.ToLower(u.Name()) + ".localtau"

	err = u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			// seer resolves DNS; give it the host binding.
			"seer":     {Others: map[string]int{"mock": 1}, Hosts: map[string]string{"admin." + fqdn: "mockhost"}},
			"mockhost": {},
		},
	})
	assert.NilError(t, err)

	dnsPort, err := u.GetPort(u.Seer().Node(), "dns")
	assert.NilError(t, err)
	addr := fmt.Sprintf("127.0.0.1:%d", dnsPort)
	client := createDnsClient("tcp")

	resolveA := func(name string) (string, bool) {
		md := new(dns.Msg)
		md.SetQuestion(name, dns.TypeA)
		resp, _, err := client.Exchange(md, addr)
		if err != nil || len(resp.Answer) != 1 {
			return "", false
		}
		a, ok := resp.Answer[0].(*dns.A)
		if !ok {
			return "", false
		}
		return a.A.String(), true
	}

	// Wait for mockhost to beacon: its direct <svc>.tau.<fqdn> name resolves.
	var directIP string
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); {
		if ip, ok := resolveA("mockhost.tau." + fqdn + "."); ok {
			directIP = ip
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	assert.Assert(t, directIP != "", "mockhost.tau.<fqdn> never resolved")

	// The bound custom domain resolves to the same node via domains.hosts.
	boundIP, ok := resolveA("admin." + fqdn + ".")
	assert.Assert(t, ok, "admin.<fqdn> did not resolve to a single A record")
	assert.Equal(t, boundIP, directIP, "admin.<fqdn> should resolve to mockhost's node")
}

// TestHttp_HostBinding_Dreaming proves the HTTP layer host-scopes a bound domain
// to its service. Dream has no shared shape config — each service gets its own
// via WithHosts — so the binding goes on BOTH seer (DNS) and mockhost (so it
// registers /whoami under the bound host). A request with the bound Host reaches
// mockhost's handler; a mismatched Host does not.
func TestHttp_HostBinding_Dreaming(t *testing.T) {
	m, err := dream.New(t.Context())
	assert.NilError(t, err)
	defer m.Close()

	u, err := m.New(dream.UniverseConfig{Name: t.Name()})
	assert.NilError(t, err)

	fqdn := strings.ToLower(u.Name()) + ".localtau"
	// Two domains bound to the same service — the route registers under both via
	// Hosts, so both must reach the handler.
	bound := []string{"admin." + fqdn, "console." + fqdn}
	binding := map[string]string{bound[0]: "mockhost", bound[1]: "mockhost"}

	err = u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"seer":     {Hosts: binding},
			"mockhost": {Hosts: binding},
		},
	})
	assert.NilError(t, err)

	svc := u.ServiceInstance("mockhost")
	assert.Assert(t, svc != nil, "mockhost did not register")
	port, err := u.GetPortHttp(svc.Node())
	assert.NilError(t, err)
	url := fmt.Sprintf("http://127.0.0.1:%d/whoami", port)

	get := func(host string) (int, string) {
		req, _ := http.NewRequest(http.MethodGet, url, nil)
		req.Host = host
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return 0, err.Error()
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(body)
	}

	// Wait for the HTTP server to be listening.
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); {
		if code, _ := get(bound[0]); code != 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Every bound host reaches mockhost's route.
	for _, host := range bound {
		code, body := get(host)
		assert.Equal(t, code, http.StatusOK, "bound host "+host+" should reach mockhost; body="+body)
		assert.Assert(t, strings.Contains(body, "mockhost"), "handler body for "+host+": "+body)
	}

	// A mismatched host does not — routes are host-scoped.
	code, _ := get("wrong." + fqdn)
	assert.Equal(t, code, http.StatusNotFound, "a mismatched host must not match the route")
}
