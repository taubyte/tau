package seer

import (
	"context"
	"net"
	"time"

	"github.com/miekg/dns"
	domainSpecs "github.com/taubyte/go-specs/domain"
)

func (h *dnsHandler) replyWithHTTPServicingNodes(ctx context.Context, w dns.ResponseWriter, r *dns.Msg, errMsg *dns.Msg, msg dns.Msg) {
	// TODO: Find a smart way to determine what to provide. For example if Seer IP is public, theres no gateways but there're substrates with private ips, return []
	nodeIps, err := h.getServiceIp(ctx, "gateway")
	if err != nil || len(nodeIps) == 0 {
		nodeIps, err = h.getServiceIp(ctx, "substrate")
		if err != nil || len(nodeIps) == 0 {
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

func (h *dnsHandler) createDomainTnsPathSlice(fqdn string) ([]string, error) {
	tnsPath := h.seer.positiveCache.Get("/tns/" + fqdn)
	if tnsPath == nil {
		_tnsPath, err := domainSpecs.Tns().BasicPath(fqdn)
		if err != nil {
			return nil, err
		}

		h.seer.positiveCache.Set("/tns/"+fqdn, _tnsPath.Slice(), 5*time.Minute)
		return _tnsPath.Slice(), nil
	}

	return tnsPath.Value(), nil
}
