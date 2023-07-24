package service

import (
	"net"
	"time"

	"github.com/miekg/dns"
	domainSpecs "github.com/taubyte/go-specs/domain"
)

const defaultFallback string = "__"

func (h *dnsHandler) replyFallback(w dns.ResponseWriter, r *dns.Msg, errMsg *dns.Msg, msg dns.Msg) {
	logger.Infof("HITTING FALLBACK FOR %s", msg.Question[0].Name)
	msg.Answer = append(msg.Answer, &dns.CNAME{
		Hdr: dns.RR_Header{
			Name:   r.Question[0].Name,
			Rrtype: dns.TypeCNAME,
			Class:  dns.ClassINET,
			Ttl:    60,
		},
		Target: defaultFallback + r.Question[0].Name,
	})

	err := w.WriteMsg(&msg)
	if err != nil {
		logger.Errorf("Failed writing fallback msg with %v", err)
	}
}

func (h *dnsHandler) reply(w dns.ResponseWriter, r *dns.Msg, errMsg *dns.Msg, msg dns.Msg) {
	nodeIps, err := h.getNodeIp()
	if err != nil || len(nodeIps) == 0 {
		h.replyFallback(w, r, errMsg, msg)
		return
	}

	switch r.Question[0].Qtype {
	case dns.TypeA:
		for _, ip := range nodeIps {
			msg.Answer = append(msg.Answer, &dns.A{
				Hdr: dns.RR_Header{Name: r.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60},
				A:   net.ParseIP(ip),
			})

		}
	}

	err = w.WriteMsg(&msg)
	if err != nil {
		logger.Errorf("write message failed with: %s", err)
		err = w.WriteMsg(errMsg)
		if err != nil {
			logger.Errorf("Failed writing error message for WriteMsg with %v", err)
		}
	}
}

func (h *dnsHandler) createDomainTnsPathSlice(fqdn string) ([]string, error) {
	tnsPath := h.cache.Get("/tns/" + fqdn)
	if tnsPath == nil {
		_tnsPath, err := domainSpecs.Tns().BasicPath(fqdn)
		if err != nil {
			return nil, err
		}

		h.cache.Set("/tns/"+fqdn, _tnsPath.Slice(), 5*time.Minute)
		return _tnsPath.Slice(), nil
	}

	return tnsPath.Value(), nil
}
