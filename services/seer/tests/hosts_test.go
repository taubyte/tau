//go:build dreaming

package tests

import (
	"fmt"
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
	commonSpecs "github.com/taubyte/tau/pkg/specs/common"

	dns "github.com/miekg/dns"
	"gotest.tools/v3/assert"

	_ "github.com/taubyte/tau/services/seer/dream"
)

// mockService is a bare service registered through the same seam ee services use
// (RegisterService + Registry.Set): it stands up a node in the universe and
// beacons under its name so seer can resolve it — nothing else. Used to test
// domains.hosts routing without coupling to a real service.
type mockService struct{ node peer.Node }

func (m *mockService) Node() peer.Node { return m.node }
func (m *mockService) Close() error    { return nil } // universe owns the node

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
	return &mockService{node: node}, nil
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
