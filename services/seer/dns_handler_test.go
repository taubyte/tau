package seer

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/ipfs/go-log/v2"
	"github.com/jellydator/ttlcache/v3"
	"github.com/miekg/dns"
	"github.com/taubyte/tau/core/services/tns"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/pkg/config"
	"golang.org/x/time/rate"
	"gotest.tools/v3/assert"
)

type noTns struct{ tns.Client }

func (noTns) Lookup(tns.Query) (interface{}, error) { return nil, errors.New("not found") }

type fakeWriter struct {
	remote net.Addr
	msgs   []*dns.Msg
}

func (w *fakeWriter) LocalAddr() net.Addr         { return w.remote }
func (w *fakeWriter) RemoteAddr() net.Addr        { return w.remote }
func (w *fakeWriter) WriteMsg(m *dns.Msg) error   { w.msgs = append(w.msgs, m); return nil }
func (w *fakeWriter) Write(b []byte) (int, error) { return len(b), nil }
func (w *fakeWriter) Close() error                { return nil }
func (w *fakeWriter) TsigStatus() error           { return nil }
func (w *fakeWriter) TsigTimersOnly(bool)         {}
func (w *fakeWriter) Hijack()                     {}
func (w *fakeWriter) last() *dns.Msg              { return w.msgs[len(w.msgs)-1] }
func udpFrom(ip string) *fakeWriter {
	return &fakeWriter{remote: &net.UDPAddr{IP: net.ParseIP(ip), Port: 5353}}
}
func tcpFrom(ip string) *fakeWriter {
	return &fakeWriter{remote: &net.TCPAddr{IP: net.ParseIP(ip), Port: 5353}}
}
func question(name string, qtype uint16) *dns.Msg { return new(dns.Msg).SetQuestion(name, qtype) }

// testHandler is a handler on a Seer that knows known.example.com (served by the
// gateway at 10.0.0.1) and the service domains *.tau.example.com, nothing else.
func testHandler(t *testing.T) *dnsHandler {
	cfg, err := config.New(config.WithServicesDomainRegExp(regexp.MustCompile(`^[^.]+\.tau\.example\.com$`)))
	assert.NilError(t, err)
	node := peer.Mock(t.Context())
	t.Cleanup(node.Close)
	s := &Service{
		node:          node,
		config:        cfg,
		tns:           noTns{},
		positiveCache: ttlcache.New[string, []string](),
		negativeCache: ttlcache.New[string, bool](),
	}
	s.positiveCache.Set("known.example.com", []string{"/domain"}, PositiveCacheTTL)
	h := newDnsHandler(s)
	h.serverIPCache.Set("gateway", []string{"10.0.0.1"}, ServerIpCacheTTL)
	h.serverIPCache.Set("substrate", []string{"10.0.0.2"}, ServerIpCacheTTL)
	return h
}

// captureLevels counts log entries per level written while f runs.
func captureLevels(t *testing.T, f func()) map[string]int {
	log.SetLogLevel("tau.seer.service", "debug")
	t.Cleanup(func() { log.SetLogLevel("tau.seer.service", "error") })
	pipe := log.NewPipeReader(log.PipeFormat(log.JSONOutput), log.PipeLevel(log.LevelDebug))
	var (
		wg     sync.WaitGroup
		levels = map[string]int{}
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		sc := bufio.NewScanner(pipe)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			for _, l := range []string{"debug", "info", "warn", "error"} {
				if strings.Contains(sc.Text(), `"level":"`+l+`"`) {
					levels[l]++
				}
			}
		}
	}()
	f()
	pipe.Close()
	wg.Wait()
	return levels
}

func TestUnregisteredQueriesLogNoErrors(t *testing.T) {
	h := testHandler(t)
	levels := captureLevels(t, func() {
		for i := range 100 {
			// 50 unknown names, each asked twice: once unregistered, once blocked
			h.ServeDNS(tcpFrom("192.0.2.1"), question(fmt.Sprintf("nope%d.other.com.", i%50), dns.TypeA))
		}
	})
	assert.Equal(t, levels["error"], 0)
	assert.Equal(t, levels["info"], 0)
	assert.Assert(t, levels["debug"] > 0)

	blocked, unregistered, top := h.flush()
	assert.Equal(t, blocked, 50)
	assert.Equal(t, unregistered, 50)
	assert.DeepEqual(t, top, []string{"nope0.other.com=2", "nope1.other.com=2", "nope10.other.com=2", "nope11.other.com=2", "nope12.other.com=2"})

	blocked, unregistered, top = h.flush()
	assert.Equal(t, blocked+unregistered+len(top), 0)
}

func TestSummaryCountsAndCapsNames(t *testing.T) {
	h := testHandler(t)
	for i := range maxCountedNames + 500 {
		h.count(fmt.Sprintf("n%d.com", i), false)
	}
	for range 7 {
		h.count("hot.com", true)
	}
	assert.Equal(t, len(h.names), maxCountedNames)

	levels := captureLevels(t, h.logSummary)
	assert.Equal(t, levels["info"], 1)

	levels = captureLevels(t, h.logSummary) // reset: nothing to say
	assert.Equal(t, levels["info"], 0)

	h.count("hot.com", true)
	h.count("cold.com", false)
	blocked, unregistered, top := h.flush()
	assert.Equal(t, blocked, 1)
	assert.Equal(t, unregistered, 1)
	assert.Equal(t, len(top), 2)
}

func TestErrorReplyIsAResponse(t *testing.T) {
	h := testHandler(t)
	for _, name := range []string{"nope.other.com.", "nope.other.com."} { // unregistered, then blocked
		w := tcpFrom("192.0.2.1")
		q := question(name, dns.TypeA)
		h.ServeDNS(w, q)
		r := w.last()
		assert.Equal(t, r.Response, true)
		assert.Equal(t, r.Id, q.Id)
		assert.Equal(t, r.Rcode, dns.RcodeNameError)
		assert.Equal(t, r.Question[0].Name, name)
	}
}

func TestAnyGetsRFC8482(t *testing.T) {
	h := testHandler(t)
	for _, name := range []string{"known.example.com.", "seer.tau.example.com."} {
		w := tcpFrom("192.0.2.1")
		h.ServeDNS(w, question(name, dns.TypeANY))
		r := w.last()
		assert.Equal(t, r.Rcode, dns.RcodeSuccess, name)
		assert.Equal(t, len(r.Answer), 1, name)
		hinfo, ok := r.Answer[0].(*dns.HINFO)
		assert.Assert(t, ok, name)
		assert.Equal(t, hinfo.Cpu, "RFC8482")
		assert.Equal(t, hinfo.Os, "")
	}
}

func TestAAAAOnKnownDomainIsNoData(t *testing.T) {
	h := testHandler(t)
	for _, name := range []string{"known.example.com.", "seer.tau.example.com."} {
		for _, qtype := range []uint16{dns.TypeAAAA, dns.TypeMX} {
			w := tcpFrom("192.0.2.1")
			h.ServeDNS(w, question(name, qtype))
			r := w.last()
			assert.Equal(t, r.Rcode, dns.RcodeSuccess, name)
			assert.Equal(t, len(r.Answer), 0, name)
			assert.Equal(t, r.Response, true)
		}
	}

	w := tcpFrom("192.0.2.1") // the A record still answers
	h.ServeDNS(w, question("known.example.com.", dns.TypeA))
	assert.Equal(t, len(w.last().Answer), 1)
}

func TestUdpBurstSlipsToTruncated(t *testing.T) {
	defer func(l rate.Limit, b int) { UdpRateLimit, UdpRateBurst = l, b }(UdpRateLimit, UdpRateBurst)
	UdpRateLimit, UdpRateBurst = 100, 200
	h := testHandler(t)
	q := question("known.example.com.", dns.TypeA)

	var answered, truncated int
	for i := range UdpRateBurst + 50 {
		w := udpFrom(fmt.Sprintf("198.51.100.%d", i%250)) // one /24
		h.ServeDNS(w, q)
		assert.Equal(t, len(w.msgs), 1, "every query gets a reply")
		r := w.last()
		assert.Equal(t, r.Id, q.Id)
		if r.Truncated {
			truncated++
			assert.Equal(t, len(r.Answer), 0)
		} else {
			answered++
		}
	}
	assert.Assert(t, answered >= UdpRateBurst && answered < UdpRateBurst+10, answered)
	assert.Assert(t, truncated > 0)

	w := udpFrom("198.51.101.1") // another /24 has its own budget
	h.ServeDNS(w, q)
	assert.Equal(t, w.last().Truncated, false)

	for range UdpRateBurst + 50 { // TCP from the limited /24 is never limited
		w := tcpFrom("198.51.100.7")
		h.ServeDNS(w, q)
		assert.Equal(t, w.last().Truncated, false)
		assert.Equal(t, len(w.last().Answer), 1)
	}

	assert.Equal(t, h.slip(udpFrom("2001:db8:1:2::1")), false) // IPv6 keyed by /56
	for range UdpRateBurst {
		h.slip(udpFrom("2001:db8:1:2::1"))
	}
	assert.Equal(t, h.slip(udpFrom("2001:db8:1:ff::9")), true)
}

// A wildcard (or any unknown label) under the service domain, like the
// *.tau CNAME or a wildcard cert lookup, gets NXDOMAIN without an error log.
func TestWildcardServiceNameIsQuiet(t *testing.T) {
	h := testHandler(t)
	for _, name := range []string{"*.tau.example.com.", "nosuch.tau.example.com."} {
		w := tcpFrom("192.0.2.1")
		levels := captureLevels(t, func() { h.ServeDNS(w, question(name, dns.TypeA)) })
		assert.Equal(t, levels["error"], 0, name)
		assert.Equal(t, w.last().Rcode, dns.RcodeNameError, name)
	}
}
