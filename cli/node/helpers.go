package node

import (
	"fmt"
	"regexp"

	commonIface "github.com/taubyte/go-interfaces/services/common"
	domainSpecs "github.com/taubyte/go-specs/domain"
	"github.com/taubyte/p2p/peer"

	libp2p "github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
)

func setNetworkDomains(conf *commonIface.GenericConfig) {
	domainSpecs.WhiteListedDomains = conf.Domains.Whitelisted.Postfix
	domainSpecs.TaubyteServiceDomain = regexp.MustCompile(conf.Domains.Services)
	domainSpecs.SpecialDomain = regexp.MustCompile(conf.Domains.Generated)
	domainSpecs.TaubyteHooksDomain = regexp.MustCompile(fmt.Sprintf(`https://patrick.tau.%s`, conf.NetworkUrl))
}

func convertToAddrInfo(peers []string) ([]libp2p.AddrInfo, error) {
	addr := make([]libp2p.AddrInfo, 0)
	for _, _addr := range peers {
		addrInfo, err := convertToMultiAddr(_addr)
		if err != nil {
			return nil, fmt.Errorf("converting `%s` to multi addr failed with: %s", _addr, err)
		}

		addr = append(addr, *addrInfo)
	}

	return addr, nil
}

func convertToMultiAddr(addr string) (*libp2p.AddrInfo, error) {
	_multiaddr, err := multiaddr.NewMultiaddr(addr)
	if err != nil {
		return nil, fmt.Errorf("converting `%s` to a multi address failed with: %s", addr, err)
	}

	addrInfo, err := libp2p.AddrInfoFromP2pAddr(_multiaddr)
	if err != nil {
		return nil, fmt.Errorf("getting addr from p2p addr failed with: %s", err)
	}

	return addrInfo, nil

}

func convertBootstrap(peers []string, devMode bool) (peer.BootstrapParams, error) {
	if devMode && len(peers) < 1 {
		return peer.StandAlone(), nil
	}

	if len(peers) > 0 {
		peers, err := convertToAddrInfo(peers)
		if err != nil {
			return peer.BootstrapParams{}, fmt.Errorf("converting peers to libp2p addr info failed with: %s", err)
		}

		return peer.Bootstrap(peers...), nil
	}

	return peer.StandAlone(), nil
}
