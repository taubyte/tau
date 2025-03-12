package auto

import (
	"context"

	"github.com/taubyte/tau/config"
	"github.com/taubyte/tau/p2p/peer"
	service "github.com/taubyte/tau/pkg/http"
	basicHttp "github.com/taubyte/tau/pkg/http/basic"
	basicHttpSecure "github.com/taubyte/tau/pkg/http/basic/secure"
	"github.com/taubyte/tau/pkg/http/options"
)

func opsFromConfig(config *config.Node) []options.Option {
	ops := []options.Option{options.Listen(config.HttpListen)}
	if config.CustomAcme {
		ops = append(ops, options.ACMEWithKey(config.AcmeUrl, config.AcmeKey))
	}
	if config.Verbose {
		ops = append(ops, options.Debug())
	}

	return ops
}

func New(ctx context.Context, node peer.Node, config *config.Node, ops ...options.Option) (http service.Service, err error) {
	ops = append(ops, opsFromConfig(config)...)
	if config.DevMode {
		return devHttp(ctx, config.EnableHTTPS, ops...)
	} else {
		return new(node, config.ClientNode, config, ops...)
	}
}

func devHttp(ctx context.Context, enableHttps bool, ops ...options.Option) (service.Service, error) {
	if !enableHttps {
		return basicHttp.New(ctx, ops...)
	} else {
		ops = append(ops, options.SelfSignedCertificate())
		return basicHttpSecure.New(ctx, ops...)
	}
}
