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

	// Keep the egress guard on the default resolver too: a guest must not be
	// able to reset and then resolve via the host's local stub (127.0.0.53), a
	// denied loopback destination.
	resolver.Resolver = &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			return netguard.RestrictedDialer(10*time.Second).DialContext(ctx, network, address)
		},
	}

	return 0
}
