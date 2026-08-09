package dns

import (
	"context"
	"net"
	"time"

	common "github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/pkg/netguard"
)

func (f *Factory) dnsNewResolver(ctx context.Context, module common.Module,
	resolverIdPtr uint32,
) uint32 {
	return uint32(f.WriteUint32Le(module, resolverIdPtr, f.generateResolver()))
}

func (f *Factory) dnsRerouteResolver(ctx context.Context, module common.Module,
	resolverId,
	addrPtr, addrLen,
	netPtr, netLen uint32,
) uint32 {
	addr, err := f.ReadString(module, addrPtr, addrLen)
	if err != 0 {
		return uint32(err)
	}

	netType, err := f.ReadString(module, netPtr, netLen)
	if err != 0 {
		return uint32(err)
	}

	resolver, err := f.getResolver(resolverId)
	if err != 0 {
		return uint32(err)
	}

	resolver.Resolver = &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			// Enforce the egress policy on the guest-chosen resolver address so a
			// custom resolver can't be pointed at node-local or metadata IPs.
			return netguard.RestrictedDialer(10*time.Second).DialContext(ctx, netType, addr)
		},
	}

	return 0
}

func (f *Factory) dnsResetResolver(ctx context.Context, module common.Module,
	resolverId uint32,
) uint32 {
	resolver, err := f.getResolver(resolverId)
	if err != 0 {
		return uint32(err)
	}

	// Back to the host's own resolver, guard and all removed. The guard belongs
	// on Reroute, where the guest picks the address; here it picks nothing, and
	// the address is whatever the node is configured with — commonly a systemd
	// stub on 127.0.0.53, which the policy denies. Guarding this would resolve
	// nothing on such a host while blocking no attack: the guest cannot aim it.
	resolver.Resolver = &net.Resolver{}

	return 0
}
