package seer

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	validate "github.com/taubyte/domain-validation"
	iface "github.com/taubyte/tau/core/services/seer"
	servicesCommon "github.com/taubyte/tau/services/common"

	"github.com/jellydator/ttlcache/v3"
	"github.com/miekg/dns"
	"github.com/taubyte/tau/pkg/specs/common"
	"golang.org/x/time/rate"
)

var (
	DnsSummaryInterval = 5 * time.Minute
	// Per source /24 (IPv4) or /56 (IPv6). Generous on purpose: Seer is
	// authoritative, so its callers are shared resolvers speaking for many
	// users; over the limit a reply asks for TCP, it is never dropped.
	UdpRateLimit rate.Limit = 1000
	UdpRateBurst            = 2000
)

const (
	maxCountedNames = 1000
	maxUdpSources   = 100000
)

type dnsHandler struct {
	seer          *Service
	serverIPCache *ttlcache.Cache[string, []string]
	limiters      *ttlcache.Cache[string, *rate.Limiter]

	mu           sync.Mutex
	blocked      int
	unregistered int
	names        map[string]int
}

// count records a refused name; it is logged in the periodic summary instead of per query.
func (h *dnsHandler) count(name string, blocked bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if blocked {
		h.blocked++
	} else {
		h.unregistered++
	}
	if name == "" {
		name = "." // the root
	}
	if _, ok := h.names[name]; ok || len(h.names) < maxCountedNames {
		h.names[name]++
	}
}

// flush returns and resets the counters; top holds the 5 most refused names.
func (h *dnsHandler) flush() (blocked, unregistered int, top []string) {
	h.mu.Lock()
	names := h.names
	blocked, unregistered = h.blocked, h.unregistered
	h.blocked, h.unregistered, h.names = 0, 0, make(map[string]int)
	h.mu.Unlock()

	for n := range names {
		top = append(top, n)
	}
	sort.Slice(top, func(i, j int) bool {
		if names[top[i]] != names[top[j]] {
			return names[top[i]] > names[top[j]]
		}
		return top[i] < top[j]
	})
	if len(top) > 5 {
		top = top[:5]
	}
	for i, n := range top {
		top[i] = fmt.Sprintf("%s=%d", n, names[n])
	}
	return
}

func (h *dnsHandler) logSummary() {
	if blocked, unregistered, top := h.flush(); blocked+unregistered > 0 {
		logger.Infof("dns: %d blocked, %d unregistered in 5m; top: %v", blocked, unregistered, top)
	}
}

func (h *dnsHandler) summarize(ctx context.Context) {
	t := time.NewTicker(DnsSummaryInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			h.logSummary()
		}
	}
}

// slip reports whether a UDP query is over its source's rate. Such queries get an
// empty truncated reply so real resolvers retry over TCP, which is never limited.
// Keyed by prefix, never dropped: callers are shared resolvers and UDP sources can be spoofed.
func (h *dnsHandler) slip(w dns.ResponseWriter) bool {
	addr, ok := w.RemoteAddr().(*net.UDPAddr)
	if !ok {
		return false
	}
	var key string
	if ip4 := addr.IP.To4(); ip4 != nil {
		key = ip4.Mask(net.CIDRMask(24, 32)).String()
	} else {
		key = addr.IP.Mask(net.CIDRMask(56, 128)).String()
	}
	it := h.limiters.Get(key)
	if it == nil {
		it, _ = h.limiters.GetOrSet(key, rate.NewLimiter(UdpRateLimit, UdpRateBurst))
	}
	return !it.Value().Allow()
}

func newDnsHandler(s *Service) *dnsHandler {
	return &dnsHandler{
		seer:          s,
		serverIPCache: ttlcache.New(ttlcache.WithTTL[string, []string](ServerIpCacheTTL), ttlcache.WithDisableTouchOnHit[string, []string]()),
		limiters:      ttlcache.New(ttlcache.WithTTL[string, *rate.Limiter](time.Minute), ttlcache.WithCapacity[string, *rate.Limiter](maxUdpSources)),
		names:         make(map[string]int),
	}
}

func (srv *dnsServer) Start(ctx context.Context) {
	go func() {
		logger.Info("Starting DNS Server on UDP")
		if err := srv.Udp.ListenAndServe(); err != nil {
			logger.Errorf("UDP DNS server failed with: %s", err.Error())
		}
	}()

	go func() {
		logger.Info("Starting DNS Server on TCP")
		if err := srv.Tcp.ListenAndServe(); err != nil {
			logger.Errorf("TCP DNS server failed with: %s", err.Error())
		}
	}()
}

func (srv *dnsServer) Stop() {
	if err := srv.Udp.Shutdown(); err != nil {
		logger.Errorf("stopping UDP server failed with: %s", err)
	}
	if err := srv.Tcp.Shutdown(); err != nil {
		logger.Errorf("stopping TCP server failed with: %s", err)
	}
}

func (s *Service) server(listen, net string, h *dnsHandler) *dns.Server {
	return &dns.Server{
		Addr:    listen,
		Net:     net,
		Handler: h,
	}
}

func (seer *Service) newDnsServer(devMode bool, port int) error {
	//Create cache nodes and spam requests
	seer.positiveCache = ttlcache.New(ttlcache.WithTTL[string, []string](PositiveCacheTTL), ttlcache.WithDisableTouchOnHit[string, []string]())
	seer.negativeCache = ttlcache.New(ttlcache.WithTTL[string, bool](DefaultBlockTime), ttlcache.WithDisableTouchOnHit[string, bool]())

	// Create TCP and UDP
	validate.UseResolver(seer.dnsResolver)
	if !devMode {
		port = servicesCommon.DefaultDnsPort
	}

	listen := ":" + strconv.Itoa(port)

	// one handler for UDP and TCP so the summary counts both
	h := newDnsHandler(seer)
	seer.dns = &dnsServer{
		Tcp:  seer.server(listen, "tcp", h),
		Udp:  seer.server(listen, "udp", h),
		Seer: seer,
	}
	go h.summarize(seer.node.Context())

	go seer.positiveCache.Start()
	go seer.negativeCache.Start()

	return nil
}

func (s *Service) isServiceOrAliasDomain(dom string) bool {
	logger.Debugf("Checking %s against %s", dom, s.config.ServicesDomainRegExp().String())
	if s.config.ServicesDomainRegExp().MatchString(dom) {
		return true
	}
	for _, r := range s.config.AliasDomainsRegExp() {
		logger.Debugf("Checking %s against %s", dom, r.String())
		if r.MatchString(dom) {
			return true
		}
	}
	return false
}

// Real DNS Handler
func (h *dnsHandler) ServeDNS(w dns.ResponseWriter, r *dns.Msg) {
	ctx, ctxC := context.WithTimeout(h.seer.Node().Context(), MaxDnsResponseTime)
	defer ctxC()

	if h.slip(w) {
		tc := new(dns.Msg).SetReply(r)
		tc.Truncated = true
		if err := w.WriteMsg(tc); err != nil {
			logger.Debugf("writing truncated reply failed with: %s", err.Error())
		}
		return
	}

	msg := dns.Msg{}
	msg.SetReply(r)
	msg.Authoritative = true

	errMsg := new(dns.Msg).SetRcode(r, dns.RcodeNameError)
	errMsg.Authoritative = true

	if len(msg.Question) > 0 {
		name := msg.Question[0].Name
		if strings.HasSuffix(msg.Question[0].Name, ".") {
			name = strings.TrimSuffix(msg.Question[0].Name, ".")
		}
		name = strings.ToLower(name)

		logger.Debugf("request for %s (type: %d)", name, msg.Question[0].Qtype)

		if h.seer.acmeChallenge != nil && name == h.seer.acmeChallengeName && msg.Question[0].Qtype == dns.TypeTXT && h.replyACMEChallenge(w, r, msg) {
			return
		}

		if spam := h.seer.negativeCache.Get(name); spam != nil {
			logger.Debugf("%s is currently blocked", name)
			h.count(name, true)
			if err := w.WriteMsg(errMsg); err != nil {
				logger.Errorf("writing error message `%s` failed with %s", errMsg, err.Error())
			}
			return
		}

		if len(msg.Question) == 0 {
			w.Close()
			return
		}

		defer func() {
			err := w.Close()
			if err != nil {
				logger.Errorf("closing dns response writer failed with: %s", err.Error())
				return
			}
		}()

		// A custom domain bound to a specific service (domains.hosts) wins:
		// resolve it to that service's nodes.
		if svc, ok := h.seer.config.ServiceForHost(name); ok {
			h.tauDnsResolve(ctx, svc, name, w, r, errMsg, msg)
			return
		}

		// if we didn't see this domain registred before
		if h.seer.positiveCache.Get(name) == nil {
			if h.seer.isServiceOrAliasDomain(name) {
				logger.Debugf("Looks like %s is a ServiceOrAliasDomain", name)
				h.tauDnsResolve(ctx, strings.Split(name, ".")[0], name, w, r, errMsg, msg)
				return
			}

			if h.seer.config.GeneratedDomainMatch(name) {
				h.replyWithHTTPServicingNodes(ctx, w, r, errMsg, msg)
				return
			}

			logger.Debugf("Checking %s against tns", name)
			// not cached, check if domain exist in tns
			if _, err := h.fetchDomainTnsPathSlice(name); err == nil {
				h.replyWithHTTPServicingNodes(ctx, w, r, errMsg, msg)
				return
			}
		} else { // we have it, don't fetch it again
			logger.Debugf("We have %s, it's a registered domain", name)
			h.replyWithHTTPServicingNodes(ctx, w, r, errMsg, msg)
			return
		}

		// Store in negative cache as spam
		logger.Debugf("%s (type: %d) is not registered", name, msg.Question[0].Qtype)
		h.count(name, false)
		h.seer.negativeCache.Set(name, true, DefaultBlockTime)
	}

	if err := w.WriteMsg(errMsg); err != nil {
		logger.Errorf("sending reply failed with: %s", err.Error())
	}

}

func (h *dnsHandler) tauDnsResolve(ctx context.Context, service, name string, w dns.ResponseWriter, r *dns.Msg, errMsg *dns.Msg, msg dns.Msg) {
	if err := common.ValidateServices([]string{service}); err != nil {
		// the label comes from the query (e.g. a wildcard *.tau lookup): unknown is spam, not a fault
		logger.Debugf("validating service `%s` failed with: %s", service, err.Error())
		if err := w.WriteMsg(errMsg); err != nil {
			logger.Errorf("writing error message `%s` failed with: %s", errMsg, err.Error())
		}
		return
	}

	switch r.Question[0].Qtype {
	case dns.TypeA:
		logger.Debugf("request for %s A", name)
		ips, err := h.getServiceIpWithCache(ctx, service, func(id string, ts int64, usage *iface.UsageData) bool {
			if h.seer.poe != nil {
				usageMap := usage.ToMap()
				usageMap["timestamp"] = ts
				ok, err := h.seer.poe.Check(id, usageMap)
				if err != nil {
					logger.Errorf("scoring %s failed with: %s", id, err.Error())
					return true
				}
				logger.Debugf("scoring %s with: %v, result: %t", id, usageMap, ok)
				return ok
			}
			return true
		})
		if err != nil {
			logger.Errorf("getting ip for %s failed with %s", service, err.Error())
			if err := w.WriteMsg(errMsg); err != nil {
				logger.Errorf("writing error message `%s` failed with %s", errMsg, err.Error())
			}
			return
		}

		for _, ip := range ips {
			msg.Answer = append(msg.Answer, &dns.A{
				Hdr: dns.RR_Header{Name: r.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: uint32(ValidServiceResponseTime.Seconds())},
				A:   net.ParseIP(ip),
			})

		}
	case dns.TypeTXT:
		txt, err := h.getServiceMultiAddr(ctx, service)
		if err != nil {
			logger.Errorf("getting txt for %s failed with %s", name, err.Error())
			if err := w.WriteMsg(errMsg); err != nil {
				logger.Errorf("writing error message `%s` failed with %s", errMsg, err.Error())
			}
			return
		}

		msg.Answer = append(msg.Answer, &dns.TXT{
			Hdr: dns.RR_Header{Name: r.Question[0].Name, Rrtype: dns.TypeTXT, Class: dns.ClassINET, Ttl: uint32(ValidServiceResponseTime.Seconds())},
			Txt: txt,
		})
	case dns.TypeCAA:
		msg.Answer = append(msg.Answer, &dns.CAA{
			Hdr:   dns.RR_Header{Name: r.Question[0].Name, Rrtype: dns.TypeCAA, Class: dns.ClassINET, Ttl: uint32(ValidServiceResponseTime.Seconds())},
			Flag:  0,
			Tag:   "issue",
			Value: h.seer.config.AcmeCAARecord(),
		})
	case dns.TypeANY:
		msg.Answer = append(msg.Answer, rfc8482(r))
	default:
		// the name exists, it just has no record of this type: NOERROR with no data
		logger.Debugf("request for %s (type: %d)", name, r.Question[0].Qtype)
	}

	err := w.WriteMsg(&msg)
	if err != nil {
		logger.Errorf("writing msg for url `%s` failed with: %s", name, err.Error())
		w.WriteMsg(errMsg)
	}
}

func (h *dnsHandler) replyWithHTTPServicingNodes(ctx context.Context, w dns.ResponseWriter, r *dns.Msg, errMsg *dns.Msg, msg dns.Msg) {
	nodeIps, err := h.getServiceIpWithCache(ctx, "gateway", func(id string, ts int64, usage *iface.UsageData) bool {
		if h.seer.poe != nil {
			usageMap := usage.ToMap()
			usageMap["timestamp"] = ts
			ok, err := h.seer.poe.Check(id, usageMap)
			if err != nil {
				// Assume the poe script has an issue & return the node anyway
				logger.Errorf("scoring %s failed with: %s", id, err.Error())
				return true
			}
			logger.Debugf("scoring %s with: %v, result: %t", id, usageMap, ok)
			return ok
		}
		return true
	})
	if err != nil || len(nodeIps) == 0 {
		nodeIps, err = h.getServiceIpWithCache(ctx, "substrate", func(id string, ts int64, usage *iface.UsageData) bool {
			if h.seer.poe != nil {
				usageMap := usage.ToMap()
				usageMap["timestamp"] = ts
				ok, err := h.seer.poe.Check(id, usageMap)
				if err != nil {
					logger.Errorf("scoring %s failed with: %s", id, err.Error())
					return true
				}
				logger.Debugf("scoring %s with: %v, result: %t", id, usageMap, ok)
				return ok
			}
			return true
		})
		if err != nil {
			err = w.WriteMsg(errMsg)
			if err != nil {
				logger.Error("writing error message for WriteMsg failed with:", err.Error())
			}
			return
		}
	}

	switch r.Question[0].Qtype {
	case dns.TypeA:
		for _, ip := range nodeIps {
			msg.Answer = append(msg.Answer, &dns.A{
				Hdr: dns.RR_Header{Name: r.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60},
				A:   net.ParseIP(ip),
			})

		}
	case dns.TypeCAA:
		msg.Answer = append(msg.Answer, &dns.CAA{
			Hdr:   dns.RR_Header{Name: r.Question[0].Name, Rrtype: dns.TypeCAA, Class: dns.ClassINET, Ttl: uint32(ValidServiceResponseTime.Seconds())},
			Flag:  0,
			Tag:   "issue",
			Value: h.seer.config.AcmeCAARecord(),
		})
	case dns.TypeANY:
		msg.Answer = append(msg.Answer, rfc8482(r))
	default:
		// the name exists, it just has no record of this type: NOERROR with no data
		logger.Debugf("request for %s (type: %d)", r.Question[0].Name, r.Question[0].Qtype)
	}

	err = w.WriteMsg(&msg)
	if err != nil {
		logger.Errorf("write message failed with: %s", err.Error())
		err = w.WriteMsg(errMsg)
		if err != nil {
			logger.Error("writing error message for WriteMsg failed with:", err.Error())
		}
	}
}

// rfc8482 is the minimal answer to ANY (RFC 8482 section 4.2).
func rfc8482(r *dns.Msg) dns.RR {
	return &dns.HINFO{
		Hdr: dns.RR_Header{Name: r.Question[0].Name, Rrtype: dns.TypeHINFO, Class: dns.ClassINET, Ttl: uint32(ValidServiceResponseTime.Seconds())},
		Cpu: "RFC8482",
	}
}
