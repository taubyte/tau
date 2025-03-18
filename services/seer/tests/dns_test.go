package tests

import (
	"fmt"
	"testing"
	"time"

	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"

	"gotest.tools/v3/assert"

	dns "github.com/miekg/dns"
	seerClient "github.com/taubyte/tau/clients/p2p/seer"

	_ "github.com/taubyte/tau/services/auth"
	_ "github.com/taubyte/tau/services/hoarder"
	_ "github.com/taubyte/tau/services/monkey"
	_ "github.com/taubyte/tau/services/patrick"
	_ "github.com/taubyte/tau/services/substrate"
)

var (
	fqdn       = "testing_website_builder.com."
	regexFqdn  = "qkfkkvlaw2.g.tau.link."
	failedFqdn = "asdhw23.g.tau.link.net."
)

func createDnsClient(net string) *dns.Client {
	c := &dns.Client{
		Net: net,
	}
	return c
}

func TestDns(t *testing.T) {
	u := dream.New(dream.UniverseConfig{Name: t.Name()})
	defer u.Stop()

	dnsPort, err := u.PortFor("seer", "dns")
	assert.NilError(t, err)
	defaultTestPort := fmt.Sprintf("127.0.0.1:%d", dnsPort)

	seerClient.DefaultUsageBeaconInterval = 100 * time.Millisecond
	seerClient.DefaultAnnounceBeaconInterval = 100 * time.Millisecond
	seerClient.DefaultGeoBeaconInterval = 100 * time.Millisecond

	err = u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"seer":      {Others: map[string]int{"dns": dnsPort, "mock": 1}},
			"tns":       {},
			"monkey":    {},
			"patrick":   {},
			"auth":      {},
			"substrate": {},
			"gateway":   {},
		},
	})
	assert.NilError(t, err)

	time.Sleep(15 * time.Second)

	// Create Tcp Client
	tcpClient := createDnsClient("tcp")
	m := new(dns.Msg)
	resolver := u.Seer().Resolver()
	cname, err := resolver.LookupCNAME(u.Context(), fqdn)
	assert.NilError(t, err)

	m.SetQuestion(cname, dns.TypeA)

	tcpResp, _, err := tcpClient.Exchange(m, defaultTestPort)
	assert.NilError(t, err)

	assert.Assert(t, len(tcpResp.Answer) == 1, "Expected 1 tcp answers got %d on tcp", len(tcpResp.Answer))

	m.SetQuestion(regexFqdn, dns.TypeA)
	tcpResp, _, err = tcpClient.Exchange(m, defaultTestPort)
	assert.NilError(t, err)

	assert.Assert(t, len(tcpResp.Answer) == 1, "Expected 1 tcp for domain regex answers got %d on tcp", len(tcpResp.Answer))

	// Expected to Fail
	m.SetQuestion(failedFqdn, dns.TypeA)
	tcpResp, _, err = tcpClient.Exchange(m, defaultTestPort)
	assert.NilError(t, err)

	assert.Assert(t, len(tcpResp.Answer) == 0, "The domain %s should have 0 answer response on tcp", failedFqdn)

	// Create Udp client
	udpClient := createDnsClient("udp")
	m = new(dns.Msg)
	m.SetQuestion(cname, dns.TypeA)

	udpResp, _, err := udpClient.Exchange(m, defaultTestPort)
	assert.NilError(t, err)

	assert.Assert(t, len(udpResp.Answer) == 1, "Expected 2 udp answers got %d on udp", len(udpResp.Answer))

	m.SetQuestion(regexFqdn, dns.TypeA)
	udpResp, _, err = udpClient.Exchange(m, defaultTestPort)
	assert.NilError(t, err)

	assert.Assert(t, len(udpResp.Answer) == 1, "Expected 2 udp for domain regex answers got %d on udp", len(udpResp.Answer))

	// Expected to fail
	m.SetQuestion(failedFqdn, dns.TypeA)
	udpResp, _, err = udpClient.Exchange(m, defaultTestPort)
	assert.NilError(t, err)

	assert.Assert(t, len(udpResp.Answer) == 0, "The domain %s should have 0 answer response on udp", failedFqdn)

	// add test here for txt records
	m.SetQuestion(cname, dns.TypeTXT)
	txtResp, _, err := udpClient.Exchange(m, defaultTestPort)
	assert.NilError(t, err)

	assert.Assert(t, len(txtResp.Answer) == 1, "Expected 1 txt answers got %d on txt", len(txtResp.Answer))

	// Get TXT record from response
	txtRecord, ok := txtResp.Answer[0].(*dns.TXT)
	assert.Assert(t, ok, "Expected TXT record")
	assert.Assert(t, len(txtRecord.Txt) > 0, "Expected non-empty TXT record")

	// Get node's multiaddrs
	nodeAddrs := u.Substrate().Node().Peer().Addrs()
	nodeID := u.Substrate().Node().ID().String()

	// Check that response matches one of the node's multiaddrs
	found := false
	for _, addr := range nodeAddrs {
		expected := addr.String() + "/p2p/" + nodeID
		for _, txt := range txtRecord.Txt {
			if txt == expected {
				found = true
				break
			}
		}
	}
	assert.Assert(t, found, "TXT record should match one of node's multiaddrs")

}
