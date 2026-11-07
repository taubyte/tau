package seer

import (
	"testing"
	"time"

	"github.com/miekg/dns"
	acmeStore "github.com/taubyte/tau/services/auth/acme/store"
	"gotest.tools/v3/assert"
)

func TestSeerAnswersACMEChallengeOnlyWhilePending(t *testing.T) {
	h := testHandler(t)
	var stored []byte
	h.seer.acmeChallengeName = "_acme-challenge.g.example.com"
	h.seer.acmeChallenge = func() ([]byte, error) { return stored, nil }

	ask := func() *dns.Msg {
		w := tcpFrom("10.9.9.9")
		h.ServeDNS(w, question("_acme-challenge.g.example.com.", dns.TypeTXT))
		return w.last()
	}

	stored = acmeStore.EncodeChallenge("dns01-value", time.Now().Add(time.Minute))
	m := ask()
	assert.Equal(t, m.Rcode, dns.RcodeSuccess)
	assert.Equal(t, len(m.Answer), 1)
	assert.DeepEqual(t, m.Answer[0].(*dns.TXT).Txt, []string{"dns01-value"})

	stored = acmeStore.EncodeChallenge("dns01-value", time.Time{}) // cleared after the order
	for _, rr := range ask().Answer {
		_, isTxt := rr.(*dns.TXT)
		assert.Assert(t, !isTxt)
	}
}
