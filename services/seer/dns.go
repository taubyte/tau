package seer

import (
	"context"
	"net"
	"strconv"
	"strings"

	validate "github.com/taubyte/domain-validation"
	servicesCommon "github.com/taubyte/tau/services/common"

	"github.com/jellydator/ttlcache/v3"
	"github.com/miekg/dns"
	"github.com/taubyte/tau/pkg/specs/common"
)

type dnsHandler struct {
	seer          *Service
	serverIPCache *ttlcache.Cache[string, []string]
}

func (srv *dnsServer) Start(ctx context.Context) {
	go func() {
		logger.Info("Starting DNS Server on UDP")
		if err := srv.Udp.ListenAndServe(); err != nil {
			panic("failed starting UDP Server error: " + err.Error())
		}
	}()

	go func() {
		logger.Info("Starting DNS Server on TCP")
		if err := srv.Tcp.ListenAndServe(); err != nil {
			panic("failed starting TCP Server error: " + err.Error())
		}
	}()
}

func (srv *dnsServer) Stop() {
	if err := srv.Udp.Shutdown(); err != nil {
		logger.Error("stopping UDP Server failed with:", err.Error())
	}
	if err := srv.Tcp.Shutdown(); err != nil {
		logger.Error("stopping TCP Server failed with:", err.Error())
	}
}

// TODO:  Why does handler point to positiveCache and negativeCache when already points to seer?
func (s *Service) server(listen, net string) *dns.Server {
	return &dns.Server{
		Addr: listen,
		Net:  net,
		Handler: &dnsHandler{
			seer:          s,
			serverIPCache: ttlcache.New(ttlcache.WithTTL[string, []string](ServerIpCacheTTL), ttlcache.WithDisableTouchOnHit[string, []string]()),
		},
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

	seer.dns = &dnsServer{
		Tcp:  seer.server(listen, "tcp"),
		Udp:  seer.server(listen, "udp"),
		Seer: seer,
	}

	go seer.positiveCache.Start()
	go seer.negativeCache.Start()

	return nil
}

func (s *Service) isServiceOrAliasDomain(dom string) bool {
	logger.Debugf("Checking %s against %s", dom, s.config.ServicesDomainRegExp.String())
	if s.config.ServicesDomainRegExp.MatchString(dom) {
		return true
	}
	for _, r := range s.config.AliasDomainsRegExp {
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

	msg := dns.Msg{}
	msg.SetReply(r)
	msg.Authoritative = true

	_errMsg := *r
	errMsg := &_errMsg
	errMsg.Rcode = dns.RcodeNameError

	if len(msg.Question) > 0 {
		name := msg.Question[0].Name
		if strings.HasSuffix(msg.Question[0].Name, ".") {
			name = strings.TrimSuffix(msg.Question[0].Name, ".")
		}
		name = strings.ToLower(name)

		logger.Debugf("request for %s (type: %d)", name, msg.Question[0].Qtype)

		if spam := h.seer.negativeCache.Get(name); spam != nil {
			logger.Errorf("%s is currently blocked", name)
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

		// if we didn't see this domain registred before
		if h.seer.positiveCache.Get(name) == nil {
			if h.seer.isServiceOrAliasDomain(name) {
				logger.Debugf("Looks like %s is a ServiceOrAliasDomain", name)
				h.tauDnsResolve(ctx, name, w, r, errMsg, msg)
				return
			}

			logger.Debugf("Checking %s against %s", name, h.seer.config.GeneratedDomainRegExp.String())
			if h.seer.config.GeneratedDomainRegExp.MatchString(name) {
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
		logger.Errorf("%s (type: %d) is not registered", name, msg.Question[0].Qtype)
		h.seer.negativeCache.Set(name, true, DefaultBlockTime)
	}

	if err := w.WriteMsg(errMsg); err != nil {
		logger.Errorf("sending reply failed with: %s", err.Error())
	}

}

func (h *dnsHandler) tauDnsResolve(ctx context.Context, name string, w dns.ResponseWriter, r *dns.Msg, errMsg *dns.Msg, msg dns.Msg) {
	service := strings.Split(name, ".")[0]
	if err := common.ValidateServices([]string{service}); err != nil {
		logger.Errorf("validating service `%s` failed with: %s", service, err.Error())
		if err := w.WriteMsg(errMsg); err != nil {
			logger.Errorf("writing error message `%s` failed with: %s", errMsg, err.Error())
		}
		return
	}

	switch r.Question[0].Qtype {
	case dns.TypeA:
		logger.Debugf("request for %s A", name)
		ips, err := h.getServiceIpWithCache(ctx, service)
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
			Value: h.seer.config.AcmeCAARecord,
		})
	default:
		logger.Debugf("request for %s (type: %d)", name, r.Question[0].Qtype)
		msg.Rcode = dns.RcodeNameError
	}

	err := w.WriteMsg(&msg)
	if err != nil {
		logger.Errorf("writing msg for url `%s` failed with: %s", name, err.Error())
		w.WriteMsg(errMsg)
	}
}

func (h *dnsHandler) replyWithHTTPServicingNodes(ctx context.Context, w dns.ResponseWriter, r *dns.Msg, errMsg *dns.Msg, msg dns.Msg) {
	// TODO: Find a smart way to determine what to provide. For example if Seer IP is public, theres no gateways but there're substrates with private ips, return []
	nodeIps, err := h.getServiceIpWithCache(ctx, "gateway")
	if err != nil || len(nodeIps) == 0 {
		nodeIps, err = h.getServiceIpWithCache(ctx, "substrate")
		if err != nil { // if no nodes, still do not return an error as the domain is valid
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
			Value: h.seer.config.AcmeCAARecord,
		})
	default:
		logger.Debugf("request for %s (type: %d)", r.Question[0].Name, r.Question[0].Qtype)
		msg.Rcode = dns.RcodeNameError
	}

	err = w.WriteMsg(&msg)
	if err != nil {
		logger.Error("write message failed with: %s", err.Error())
		err = w.WriteMsg(errMsg)
		if err != nil {
			logger.Error("writing error message for WriteMsg failed with:", err.Error())
		}
	}
}
