package seer

import (
	"github.com/miekg/dns"
	acmeStore "github.com/taubyte/tau/services/auth/acme/store"
)

// replyACMEChallenge answers the wildcard's DNS-01 TXT while an order is pending.
// It reports false when nothing is pending, so the name resolves as before.
func (h *dnsHandler) replyACMEChallenge(w dns.ResponseWriter, r *dns.Msg, msg dns.Msg) bool {
	v, err := h.seer.acmeChallenge()
	if err != nil {
		return false
	}
	txt, ok := acmeStore.DecodeChallenge(v)
	if !ok {
		return false
	}
	msg.Answer = append(msg.Answer, &dns.TXT{
		Hdr: dns.RR_Header{Name: r.Question[0].Name, Rrtype: dns.TypeTXT, Class: dns.ClassINET, Ttl: 0},
		Txt: []string{txt},
	})
	if err := w.WriteMsg(&msg); err != nil {
		logger.Errorf("writing acme challenge failed with: %s", err.Error())
	}
	return true
}
