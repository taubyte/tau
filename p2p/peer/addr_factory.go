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
func (p *node) SimpleAddrsFactory(announce []string, override bool) libp2p.Option {
	announceAddrs := make([]ma.Multiaddr, 0, len(announce))
	for _, a := range announce {
		addr, err := ma.NewMultiaddr(a)
		if err == nil {
			announceAddrs = append(announceAddrs, addr)
		}
	}

	return libp2p.AddrsFactory(func(allAddrs []ma.Multiaddr) []ma.Multiaddr {
		addrs := make([]ma.Multiaddr, 0, len(announceAddrs))
		addrs = append(addrs, announceAddrs...)
		if !override {
			addrs = append(addrs, allAddrs...)
		}

		return addrs
	})
}
