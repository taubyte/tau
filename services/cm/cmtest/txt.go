// Package cmtest is the public DNS a custom domain's proof is looked up in, for a
// test: cm asks it through its `resolver` config.
package cmtest

import (
	"net"
	"strings"
	"sync"
	"testing"

	"github.com/miekg/dns"
)

// Zone is a DNS server on loopback answering TXT with what a test set.
type Zone struct {
	// Addr is the server's host:port, what cm's resolver names.
	Addr string

	mu sync.Mutex
	m  map[string]string
}

// Serve starts a Zone, shut down with the test.
func Serve(t testing.TB) *Zone {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	z := &Zone{Addr: pc.LocalAddr().String(), m: map[string]string{}}
	server := &dns.Server{PacketConn: pc, Handler: z}
	go server.ActivateAndServe()
	t.Cleanup(func() { server.Shutdown() })
	return z
}

// Set makes value the one TXT record at name.
func (z *Zone) Set(name, value string) {
	z.mu.Lock()
	defer z.mu.Unlock()
	z.m[strings.ToLower(name)] = value
}

// ServeDNS answers a TXT question with what was set, as one record whose strings are
// at most 255 bytes, which a resolver joins back; anything else is NXDOMAIN.
func (z *Zone) ServeDNS(w dns.ResponseWriter, r *dns.Msg) {
	m := new(dns.Msg)
	m.SetReply(r)
	z.mu.Lock()
	for _, q := range r.Question {
		value, ok := z.m[strings.ToLower(strings.TrimSuffix(q.Name, "."))]
		if !ok || q.Qtype != dns.TypeTXT {
			continue
		}
		var parts []string
		for len(value) > 255 {
			parts, value = append(parts, value[:255]), value[255:]
		}
		parts = append(parts, value)
		m.Answer = append(m.Answer, &dns.TXT{Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypeTXT, Class: dns.ClassINET}, Txt: parts})
	}
	z.mu.Unlock()
	if len(m.Answer) == 0 {
		m.Rcode = dns.RcodeNameError
	}
	w.WriteMsg(m)
}
