package options

import (
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/pkg/http/options"
)

type OptionChecker struct {
	Checker func(host string) bool
}

func CustomDomainChecker(checker func(host string) bool) options.Option {
	return func(s options.Configurable) error {
		return s.SetOption(OptionChecker{Checker: checker})
	}
}

// OptionAutoTrust marks hosts that bypass TNS validation entirely (service
// domains, alias domains). Returns true → cert request allowed without
// TNS/TXT proof.
type OptionAutoTrust struct {
	Fn func(host string) bool
}

func AutoTrustDomain(fn func(host string) bool) options.Option {
	return func(s options.Configurable) error {
		return s.SetOption(OptionAutoTrust{Fn: fn})
	}
}

// OptionSkipDomainProof marks hosts that still go through TNS registration
// but skip the per-project DNS TXT proof step (generated subdomains).
type OptionSkipDomainProof struct {
	Fn func(host string) bool
}

func SkipDomainProof(fn func(host string) bool) options.Option {
	return func(s options.Configurable) error {
		return s.SetOption(OptionSkipDomainProof{Fn: fn})
	}
}

// OptionClientNode overrides the libp2p node used for the auth + tns + acme
// stream clients. Default: same node passed to auto.New.
type OptionClientNode struct {
	Node peer.Node
}

func ClientNode(node peer.Node) options.Option {
	return func(s options.Configurable) error {
		return s.SetOption(OptionClientNode{Node: node})
	}
}
