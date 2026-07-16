package dns

import (
	"context"
	"net"

	common "github.com/taubyte/tau/core/vm"
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
			d := net.Dialer{}
			return d.DialContext(ctx, netType, addr)
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

	resolver.Resolver = &net.Resolver{}

	return 0
}
