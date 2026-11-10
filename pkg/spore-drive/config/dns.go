package config

import (
	"net"
	"slices"
	"strings"
)

// DNSRecord is one record the cloud needs in its parent DNS zone.
// Name is relative to the zone ("@" for the apex).
type DNSRecord struct {
	Name  string
	Type  string
	Value string
	TTL   int
}

const dnsTTL = 1800

// DNSRecords returns the records to create in zone (the zone that serves the
// root domain; "" means the root domain is the zone apex): seer A records,
// tau NS, the generated wildcard CNAME and the _acme-challenge NS the wildcard
// certificate needs. The last two are left out when the generated domain is
// under tau.<root>, which the tau NS already delegates to the Seers.
func DNSRecords(p Parser, zone string) []DNSRecord {
	root := strings.TrimSuffix(p.Cloud().Domain().Root(), ".")
	gen := strings.TrimSuffix(p.Cloud().Domain().Generated(), ".")
	if zone == "" {
		zone = root
	}
	rel := func(fqdn string) string {
		if fqdn == zone {
			return "@"
		}
		return strings.TrimSuffix(fqdn, "."+zone)
	}
	seer := "seer." + root

	var recs []DNSRecord
	for _, ip := range seerIPs(p) {
		recs = append(recs, DNSRecord{rel(seer), "A", ip, dnsTTL})
	}
	recs = append(recs, DNSRecord{rel("tau." + root), "NS", seer + ".", dnsTTL})
	if gen != "" && gen != "tau."+root && !strings.HasSuffix(gen, ".tau."+root) {
		recs = append(recs,
			DNSRecord{rel("*." + gen), "CNAME", "substrate.tau." + root + ".", dnsTTL},
			DNSRecord{rel("_acme-challenge." + gen), "NS", seer + ".", dnsTTL},
		)
	}
	return recs
}

// seerIPs returns the sorted, unique addresses of hosts running a shape with seer.
func seerIPs(p Parser) (ips []string) {
	for _, hn := range p.Hosts().List() {
		h := p.Hosts().Host(hn)
		if !slices.ContainsFunc(h.Shapes().List(), func(s string) bool {
			return slices.Contains(p.Shapes().Shape(s).Services().List(), "seer")
		}) {
			continue
		}
		for _, a := range h.Addresses().List() {
			if ip, _, err := net.ParseCIDR(a); err == nil {
				a = ip.String()
			}
			if !slices.Contains(ips, a) {
				ips = append(ips, a)
			}
		}
	}
	slices.Sort(ips)
	return
}
