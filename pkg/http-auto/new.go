package auto

import (
	"context"

	service "github.com/taubyte/http"
	basicHttp "github.com/taubyte/http/basic"
	basicHttpSecure "github.com/taubyte/http/basic/secure"
	"github.com/taubyte/http/options"
	"github.com/taubyte/tau/config"
	"github.com/taubyte/tau/p2p/peer"
)

func opsFromConfig(config *config.Node) []options.Option {
	ops := []options.Option{options.Listen(config.HttpListen)}
	if config.Verbose {
		ops = append(ops, options.Debug())
	}

	return ops
}

func NewAuto(ctx context.Context, node peer.Node, config *config.Node, ops ...options.Option) (http service.Service, err error) {
	ops = append(ops, opsFromConfig(config)...)
	if config.DevMode {
		return devHttp(ctx, config.EnableHTTPS, ops...)
	} else {
		return New(node, config.ClientNode, config, ops...)
	}
}

func NewBasic(ctx context.Context, config *config.Node, ops ...options.Option) (http service.Service, err error) {
	ops = append(ops, opsFromConfig(config)...)
	if config.DevMode {
		return devHttp(ctx, config.EnableHTTPS, ops...)
	} else {
		return basicHttpSecure.New(ctx, ops...)
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
