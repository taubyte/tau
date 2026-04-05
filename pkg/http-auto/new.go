package auto

import (
	"context"

	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/pkg/config"
	service "github.com/taubyte/tau/pkg/http"
	basicHttp "github.com/taubyte/tau/pkg/http/basic"
	basicHttpSecure "github.com/taubyte/tau/pkg/http/basic/secure"
	"github.com/taubyte/tau/pkg/http/options"
)

func opsFromConfig(cfg config.Config) []options.Option {
	ops := []options.Option{options.Listen(cfg.HttpListen())}
	if cfg.CustomAcme() {
		ops = append(ops, options.ACMEWithKey(cfg.AcmeUrl(), cfg.AcmeKey()))
	}
	if cfg.Verbose() {
		ops = append(ops, options.Debug())
	}

	return ops
}

func New(ctx context.Context, node peer.Node, cfg config.Config, ops ...options.Option) (http service.Service, err error) {
	ops = append(ops, opsFromConfig(cfg)...)
	if cfg.DevMode() {
		return devHttp(ctx, cfg.EnableHTTPS(), ops...)
	}
	return new(node, cfg.ClientNode(), cfg, ops...)
}

func devHttp(ctx context.Context, enableHttps bool, ops ...options.Option) (service.Service, error) {
	if !enableHttps {
		return basicHttp.New(ctx, ops...)
	} else {
		ops = append(ops, options.SelfSignedCertificate())
		return basicHttpSecure.New(ctx, ops...)
	}
}
