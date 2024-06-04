package dns

import (
	"context"
	"net"

	"github.com/taubyte/go-sdk/errno"
	common "github.com/taubyte/tau/core/vm"
)

func (f *Factory) W_dnsNewResolver(ctx context.Context, module common.Module,
	resolverIdPtr uint32,
) errno.Error {
	return f.WriteUint32Le(module, resolverIdPtr, f.generateResolver())
}

func (f *Factory) W_dnsRerouteResolver(ctx context.Context, module common.Module,
	resolverId,
	addrPtr, addrLen,
	netPtr, netLen uint32,
) errno.Error {
	addr, err := f.ReadString(module, addrPtr, addrLen)
	if err != 0 {
		return err
	}

	netType, err := f.ReadString(module, netPtr, netLen)
	if err != 0 {
		return err
	}

	resolver, err := f.getResolver(resolverId)
	if err != 0 {
		return err
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

func (f *Factory) W_dnsResetResolver(ctx context.Context, module common.Module,
	resolverId uint32,
) errno.Error {
	resolver, err := f.getResolver(resolverId)
	if err != 0 {
		return err
	}

	resolver.Resolver = &net.Resolver{}

	return 0
}
