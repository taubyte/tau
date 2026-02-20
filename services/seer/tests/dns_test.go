//go:build dreaming

package tests

import (
	"fmt"
	"testing"
	"time"

	seerClient "github.com/taubyte/tau/clients/p2p/seer"
	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"

	"gotest.tools/v3/assert"

	dns "github.com/miekg/dns"

	_ "github.com/taubyte/tau/services/auth/dream"
	_ "github.com/taubyte/tau/services/hoarder/dream"
	_ "github.com/taubyte/tau/services/monkey/dream"
	_ "github.com/taubyte/tau/services/patrick/dream"
	_ "github.com/taubyte/tau/services/substrate/dream"
)

var (
	fqdn       = "testing_website_builder.com."
	regexFqdn  = "qkfkkvlaw2.g.testdns_dreaming.localtau."
	failedFqdn = "asdhw23.g.test.localtau."
)

func createDnsClient(net string) *dns.Client {
	c := &dns.Client{
		Net: net,
	}
	return c
}

func TestDns_Dreaming(t *testing.T) {
	seerClient.DefaultUsageBeaconInterval = 100 * time.Millisecond
	seerClient.DefaultAnnounceBeaconInterval = 100 * time.Millisecond
	seerClient.DefaultGeoBeaconInterval = 100 * time.Millisecond

	m, err := dream.New(t.Context())
	assert.NilError(t, err)
	defer m.Close()

	u, err := m.New(dream.UniverseConfig{Name: t.Name()})
	assert.NilError(t, err)

	err = u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"seer":      {Others: map[string]int{"mock": 1}},
			"substrate": {},
		},
	})
	assert.NilError(t, err)

	dnsPort, err := u.GetPort(u.Seer().Node(), "dns")
	assert.NilError(t, err)

	defaultTestPort := fmt.Sprintf("127.0.0.1:%d", dnsPort)

	// Create Tcp Client
	tcpClient := createDnsClient("tcp")
	md := new(dns.Msg)
	md.SetQuestion("substrate.tau.testdns_dreaming.localtau.", dns.TypeA)

	// Wait for services to start and register
	for {
		resp, _, err := tcpClient.Exchange(md, defaultTestPort)
		if err == nil && len(resp.Answer) > 0 {
			break
		}
		time.Sleep(time.Second)
	}

	resolver := u.Seer().Resolver()
	cname, err := resolver.LookupCNAME(u.Context(), fqdn)
	assert.NilError(t, err)

	md.SetQuestion(cname, dns.TypeA)

	tcpResp, _, err := tcpClient.Exchange(md, defaultTestPort)
	assert.NilError(t, err)

	assert.Assert(t, len(tcpResp.Answer) == 1, "Expected 1 tcp answers got %d on tcp", len(tcpResp.Answer))

	md.SetQuestion(regexFqdn, dns.TypeA)
	tcpResp, _, err = tcpClient.Exchange(md, defaultTestPort)
	assert.NilError(t, err)

	assert.Assert(t, len(tcpResp.Answer) == 1, "Expected 1 tcp for domain regex answers got %d on tcp", len(tcpResp.Answer))

	// Expected to Fail
	md.SetQuestion(failedFqdn, dns.TypeA)
	_, _, err = tcpClient.Exchange(md, defaultTestPort)
	assert.Assert(t, err != nil, "Expected error on tcp", err)

	// Create Udp client
	udpClient := createDnsClient("udp")
	md = new(dns.Msg)
	md.SetQuestion(cname, dns.TypeA)

	udpResp, _, err := udpClient.Exchange(md, defaultTestPort)
	assert.NilError(t, err)

	assert.Assert(t, len(udpResp.Answer) == 1, "Expected 2 udp answers got %d on udp", len(udpResp.Answer))

	md.SetQuestion(regexFqdn, dns.TypeA)
	udpResp, _, err = udpClient.Exchange(md, defaultTestPort)
	assert.NilError(t, err)

	assert.Assert(t, len(udpResp.Answer) == 1, "Expected 2 udp for domain regex answers got %d on udp", len(udpResp.Answer))

	// Expected to fail
	md.SetQuestion(failedFqdn, dns.TypeA)
	_, _, err = udpClient.Exchange(md, defaultTestPort)
	assert.Assert(t, err != nil, "Expected error on udp", err)

	// add test here for txt records
	md.SetQuestion(cname, dns.TypeTXT)
	txtResp, _, err := udpClient.Exchange(md, defaultTestPort)
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
