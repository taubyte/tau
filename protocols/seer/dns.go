package seer

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	validate "github.com/taubyte/domain-validation"
	"github.com/taubyte/go-interfaces/services/tns"
	domainSpecs "github.com/taubyte/go-specs/domain"
	protocolsCommon "github.com/taubyte/tau/protocols/common"

	"github.com/jellydator/ttlcache/v3"
	"github.com/miekg/dns"
)

// TODO: Implement a spam cache that blocks spam dns request
type dnsHandler struct {
	seer          *Service
	cache         *ttlcache.Cache[string, []string]
	negativeCache *ttlcache.Cache[string, bool]
}

func (srv *dnsServer) Start(ctx context.Context) {
	go func() {
		logger.Info("Starting DNS Server on UDP")
		if err := srv.Udp.ListenAndServe(); err != nil {
			errorMsg := fmt.Sprintf("failed starting UPD Server error: %v", err)
			panic(errors.New(errorMsg))
		}
	}()

	go func() {
		logger.Info("Starting DNS Server on TCP")
		if err := srv.Tcp.ListenAndServe(); err != nil {
			errorMsg := fmt.Sprintf("failed starting TCP Server error: %v", err)
			panic(errors.New(errorMsg))
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

func (seer *Service) newDnsServer(devMode bool, port int) error {
	//Create cache nodes and spam requests
	seer.positiveCache = ttlcache.New(ttlcache.WithTTL[string, []string](5*time.Minute), ttlcache.WithDisableTouchOnHit[string, []string]())
	seer.negativeCache = ttlcache.New(ttlcache.WithTTL[string, bool](DefaultBlockTime), ttlcache.WithDisableTouchOnHit[string, bool]())

	// Create TCP and UDP
	var s *dnsServer
	validate.UseResolver(seer.dnsResolver)
	if devMode {
		s = &dnsServer{
			Tcp:  &dns.Server{Addr: ":" + strconv.Itoa(port), Net: "tcp"},
			Udp:  &dns.Server{Addr: ":" + strconv.Itoa(port), Net: "udp"},
			Seer: seer,
		}
	} else {
		s = &dnsServer{
			Tcp:  &dns.Server{Addr: ":" + strconv.Itoa(protocolsCommon.DefaultDnsPort), Net: "tcp"},
			Udp:  &dns.Server{Addr: ":" + strconv.Itoa(protocolsCommon.DefaultDnsPort), Net: "udp"},
			Seer: seer,
		}
	}

	seer.dns = s
	s.Tcp.Handler = &dnsHandler{seer: seer, cache: seer.positiveCache, negativeCache: seer.negativeCache}
	s.Udp.Handler = &dnsHandler{seer: seer, cache: seer.positiveCache, negativeCache: seer.negativeCache}

	go seer.positiveCache.Start()
	go seer.negativeCache.Start()

	return nil
}

// Real DNS Handler
func (h *dnsHandler) ServeDNS(w dns.ResponseWriter, r *dns.Msg) {
	msg := dns.Msg{}
	msg.SetReply(r)
	msg.Authoritative = true

	_errMsg := *r
	errMsg := &_errMsg
	errMsg.Rcode = dns.RcodeNameError

	if len(msg.Question) < 1 {
		logger.Error("msg question is empty")
	}
	if spam := h.negativeCache.Get(msg.Question[0].Name); spam != nil {
		logger.Errorf("%s is currently blocked", msg.Question[0].Name)
		if err := w.WriteMsg(errMsg); err != nil {
			logger.Errorf("writing error message `%s` failed with %s", errMsg, err.Error())
		}
		return
	}

	if msg.Question == nil || len(msg.Question) == 0 {
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

	name := msg.Question[0].Name
	if strings.HasSuffix(msg.Question[0].Name, ".") {
		name = strings.TrimSuffix(msg.Question[0].Name, ".")
	}
	name = strings.ToLower(name)

	// First Case -> check if it matches .g.tau.link generated domain
	if domainSpecs.SpecialDomain.MatchString(name) {
		h.reply(w, r, errMsg, msg)
		return
	}

	// Second Case -> check if domain is under our white listed domain
	for _, domain := range domainSpecs.WhiteListedDomains {
		if name == domain {
			h.reply(w, r, errMsg, msg)
			return
		}
	}

	if domainSpecs.TaubyteServiceDomain.MatchString(name) || h.seer.caaRecordBypass.MatchString(name) {
		h.tauDnsResolve(name, w, r, errMsg, msg)
		return
	}

	// Third case ->  check if domain exist in tns
	tnsPathSlice, err := h.createDomainTnsPathSlice(name)
	if err != nil {
		logger.Errorf("createDomainTnsPathSlice for %s with: %s", name, err.Error())
		if err := w.WriteMsg(errMsg); err != nil {
			logger.Errorf("writing error message `%s` failed with: %s", errMsg, err.Error())
		}
		return
	}

	tnsInterface, err := h.seer.tns.Lookup(tns.Query{
		Prefix: tnsPathSlice,
		RegEx:  false,
	})
	if err == nil {
		domPath, ok := tnsInterface.([]string)
		if !ok {
			logger.Error("failed converting tns interface to []string")
			return
		}

		if len(domPath) != 0 {
			h.reply(w, r, errMsg, msg)
			return
		}
	}

	logger.Errorf("%s is not registered in taubyte", name)

	// Store in negative cache as spam
	h.negativeCache.Set(msg.Question[0].Name, true, DefaultBlockTime)

	err = w.WriteMsg(errMsg)
	if err != nil {
		logger.Errorf("writing error msg in ServeDns failed with: %s", err.Error())
	}
}

// TODO: Clean this up, repetitive code
func (h *dnsHandler) tauDnsResolve(name string, w dns.ResponseWriter, r *dns.Msg, errMsg *dns.Msg, msg dns.Msg) {
	service := strings.Split(name, ".")[0]
	ips, err := h.getServiceIp(service)
	if err != nil {
		logger.Errorf("getting ip for %s failed with %s", service, err.Error())
		if err := w.WriteMsg(errMsg); err != nil {
			logger.Errorf("writing error message `%s` failed with %s", errMsg, err.Error())
		}
		return
	}

	switch r.Question[0].Qtype {
	case dns.TypeA:
		for _, ip := range ips {
			msg.Answer = append(msg.Answer, &dns.A{
				Hdr: dns.RR_Header{Name: r.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60},
				A:   net.ParseIP(ip),
			})

		}
	}

	err = w.WriteMsg(&msg)
	if err != nil {
		logger.Errorf("writing msg for url `%s` failed with: %s", name, err.Error())
		w.WriteMsg(errMsg)
	}
}
