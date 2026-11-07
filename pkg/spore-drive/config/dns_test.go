package config

import (
	"testing"

	"github.com/spf13/afero"
	"gotest.tools/v3/assert"
)

func dnsTestParser(t *testing.T, root, gen string) Parser {
	p, err := New(afero.NewMemMapFs(), "/")
	assert.NilError(t, err)
	assert.NilError(t, p.Cloud().Domain().SetRoot(root))
	assert.NilError(t, p.Cloud().Domain().SetGenerated(gen))
	assert.NilError(t, p.Shapes().Shape("all").Services().Set("auth", "seer"))
	assert.NilError(t, p.Shapes().Shape("web").Services().Set("gateway"))
	for name, addr := range map[string]string{"n1": "1.1.1.1/24", "n2": "2.2.2.2"} {
		h := p.Hosts().Host(name)
		assert.NilError(t, h.Addresses().Add(addr))
		assert.NilError(t, h.Shapes().Instance("all").GenerateKey())
	}
	w := p.Hosts().Host("w1")
	assert.NilError(t, w.Addresses().Add("9.9.9.9"))
	assert.NilError(t, w.Shapes().Instance("web").GenerateKey())
	return p
}

func TestDNSRecordsSubdomainRoot(t *testing.T) {
	p := dnsTestParser(t, "t3.example.com", "gen.t3.example.com")
	assert.DeepEqual(t, DNSRecords(p, "example.com"), []DNSRecord{
		{Name: "seer.t3", Type: "A", Value: "1.1.1.1", TTL: 1800},
		{Name: "seer.t3", Type: "A", Value: "2.2.2.2", TTL: 1800},
		{Name: "tau.t3", Type: "NS", Value: "seer.t3.example.com.", TTL: 1800},
		{Name: "*.gen.t3", Type: "CNAME", Value: "substrate.tau.t3.example.com.", TTL: 1800},
		{Name: "_acme-challenge.gen.t3", Type: "NS", Value: "seer.t3.example.com.", TTL: 1800},
	})
}

func TestDNSRecordsApexRoot(t *testing.T) {
	p := dnsTestParser(t, "example.com", "g.example.com")
	r := DNSRecords(p, "")
	assert.DeepEqual(t, r[len(r)-1], DNSRecord{Name: "_acme-challenge.g", Type: "NS", Value: "seer.example.com.", TTL: 1800})
}

func TestDNSRecordsGeneratedDelegated(t *testing.T) {
	p := dnsTestParser(t, "example.com", "g.tau.example.com")
	for _, r := range DNSRecords(p, "example.com") {
		assert.Assert(t, r.Type != "CNAME" && r.Name != "_acme-challenge.g.tau", r)
	}
}
