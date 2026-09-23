package peer

import (
	"github.com/libp2p/go-libp2p"
	p2pbhost "github.com/libp2p/go-libp2p/p2p/host/basic"
	ma "github.com/multiformats/go-multiaddr"
	mamask "github.com/whyrusleeping/multiaddr-filter"
)

func makeAddrsFactory(announce []string, noAnnounce []string) (p2pbhost.AddrsFactory, error) {
	var annAddrs []ma.Multiaddr
	for _, addr := range announce {
		maddr, err := ma.NewMultiaddr(addr)
		if err != nil {
			return nil, err
		}
		annAddrs = append(annAddrs, maddr)
	}

	filters := ma.NewFilters()
	noAnnAddrs := map[string]bool{}
	for _, addr := range noAnnounce {
		f, err := mamask.NewMask(addr)
		if err == nil {
			filters.AddFilter(*f, ma.ActionDeny)
			continue
		}
		maddr, err := ma.NewMultiaddr(addr)
		if err != nil {
			return nil, err
		}
		noAnnAddrs[string(maddr.Bytes())] = true
	}

	return func(allAddrs []ma.Multiaddr) []ma.Multiaddr {
		var addrs []ma.Multiaddr
		if len(annAddrs) > 0 {
			addrs = annAddrs
		} else {
			addrs = allAddrs
		}

		var out []ma.Multiaddr
		for _, maddr := range addrs {
			// check for exact matches
			ok := noAnnAddrs[string(maddr.Bytes())]
			// check for /ipcidr matches
			if !ok && !filters.AddrBlocked(maddr) {
				out = append(out, maddr)
			}
		}
		return out
	}, nil
}

func IpfsStyleAddrsFactory(announce []string, noAnnounce []string) libp2p.Option {
	addrsFactory, err := makeAddrsFactory(announce, noAnnounce)
	if err != nil {
		return nil
	}
	return libp2p.AddrsFactory(addrsFactory)
}

// override will make the factory ignore discovered addresses
//
//	only use for public nodes
//
// A ZERO PORT IN AN ANNOUNCE MEANS "WHATEVER I BOUND", and is the one case
// where the host's own addresses are announced even under override. Listening
// on /tcp/0 is how a caller asks the kernel to allocate, and the allocation
// only exists once the host is up — long after this option was handed to
// libp2p. Announcing /tcp/0 verbatim would publish an address nothing can
// dial, so such an entry stands for the real listen addresses instead, which
// the factory receives on every call.
func (p *node) SimpleAddrsFactory(announce []string, override bool) libp2p.Option {
	announceAddrs := make([]ma.Multiaddr, 0, len(announce))
	var announceBound bool
	for _, a := range announce {
		addr, err := ma.NewMultiaddr(a)
		if err != nil {
			continue
		}
		if hasUnboundPort(addr) {
			announceBound = true
			continue
		}
		announceAddrs = append(announceAddrs, addr)
	}

	return libp2p.AddrsFactory(func(allAddrs []ma.Multiaddr) []ma.Multiaddr {
		addrs := make([]ma.Multiaddr, 0, len(announceAddrs)+len(allAddrs))
		addrs = append(addrs, announceAddrs...)
		if announceBound || !override {
			addrs = append(addrs, allAddrs...)
		}

		return addrs
	})
}

// hasUnboundPort says whether an address names port 0 — a request for a port
// rather than a port.
func hasUnboundPort(addr ma.Multiaddr) bool {
	for _, proto := range []int{ma.P_TCP, ma.P_UDP} {
		if port, err := addr.ValueForProtocol(proto); err == nil && port == "0" {
			return true
		}
	}

	return false
}
