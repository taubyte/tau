package tests

import (
	"fmt"
	"testing"
	"time"

	commonDreamland "github.com/taubyte/dreamland/core/common"
	dreamland "github.com/taubyte/dreamland/core/services"
	commonIface "github.com/taubyte/go-interfaces/common"
	"github.com/taubyte/tau/protocols/common"

	dns "github.com/miekg/dns"

	_ "github.com/taubyte/tau/protocols/auth"
	_ "github.com/taubyte/tau/protocols/hoarder"
	_ "github.com/taubyte/tau/protocols/monkey"
	_ "github.com/taubyte/tau/protocols/patrick"
	_ "github.com/taubyte/tau/protocols/substrate"
)

var (
	defaultTestPort = fmt.Sprintf("127.0.0.1:%d", common.DefaultDevDnsPort)
	fqdn            = "testing_website_builder.com."
	regexFqdn       = "qkfkkvlaw2.g.tau.link."
	failedFqdn      = "asdhw23.g.tau.link.net."
)

func createDnsClient(net string) *dns.Client {
	c := &dns.Client{
		Net: net,
	}
	return c
}

func TestDns(t *testing.T) {
	u := dreamland.Multiverse("seerDNS_test")
	defer u.Stop()
	err := u.StartWithConfig(&commonDreamland.Config{
		Services: map[string]commonIface.ServiceConfig{
			"seer":      {Others: map[string]int{"dns": common.DefaultDevDnsPort, "mock": 1}},
			"tns":       {},
			"monkey":    {},
			"patrick":   {},
			"auth":      {},
			"substrate": {},
		},
	})
	if err != nil {
		t.Error(err)
		return
	}

	time.Sleep(5 * time.Second)

	// Create Tcp Client
	tcpClient := createDnsClient("tcp")
	m := new(dns.Msg)
	resolver := u.Seer().Resolver()
	cname, err := resolver.LookupCNAME(u.Context(), fqdn)
	if err != nil {
		t.Error(err)
		return
	}

	m.SetQuestion(cname, dns.TypeA)

	tcpResp, _, err := tcpClient.Exchange(m, defaultTestPort)
	if err != nil {
		t.Errorf("Failed tcp exchange error: %v", err)
		return
	}

	if len(tcpResp.Answer) != 1 {
		t.Errorf("Expected 2 tcp answers got %d on tcp", len(tcpResp.Answer))
		return
	}

	m.SetQuestion(regexFqdn, dns.TypeA)
	tcpResp, _, err = tcpClient.Exchange(m, defaultTestPort)
	if err != nil {
		t.Errorf("Failed tcp exchange error: %v", err)
		return
	}

	if len(tcpResp.Answer) != 1 {
		t.Errorf("Expected 1 tcp for domain regex answers got %d on tcp", len(tcpResp.Answer))
		return
	}

	// Expected to Fail
	m.SetQuestion(failedFqdn, dns.TypeA)
	tcpResp, _, err = tcpClient.Exchange(m, defaultTestPort)
	if err != nil {
		t.Errorf("Failed tcp exchange error: %v", err)
		return
	}

	if len(tcpResp.Answer) > 0 {
		t.Errorf("The domain %s should have 0 answer reponse on tcp", failedFqdn)
		return
	}

	// Create Udp client
	udpClient := createDnsClient("udp")
	m = new(dns.Msg)
	m.SetQuestion(cname, dns.TypeA)

	udpResp, _, err := udpClient.Exchange(m, defaultTestPort)
	if err != nil {
		t.Errorf("Failed udp exchange error: %v", err)
		return
	}

	if len(udpResp.Answer) != 1 {
		t.Errorf("Expected 2 upd answers got %d on udp", len(udpResp.Answer))
		return
	}

	m.SetQuestion(regexFqdn, dns.TypeA)
	udpResp, _, err = udpClient.Exchange(m, defaultTestPort)
	if err != nil {
		t.Errorf("Failed udp exchange error: %v", err)
		return
	}

	if len(udpResp.Answer) != 1 {
		t.Errorf("Expected 2 upd for domain regex answers got %d on udp", len(udpResp.Answer))
		return
	}

	// Expected to fail
	m.SetQuestion(failedFqdn, dns.TypeA)
	udpResp, _, err = udpClient.Exchange(m, defaultTestPort)
	if err != nil {
		t.Errorf("Failed udp exchange error: %v", err)
		return
	}

	if len(udpResp.Answer) > 0 {
		t.Errorf("The domain %s should have 0 answer reponse on udp", failedFqdn)
		return
	}
}
