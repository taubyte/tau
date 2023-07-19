package auto

import (
	"context"
	"fmt"

	"github.com/taubyte/go-interfaces/services/common"
	service "github.com/taubyte/go-interfaces/services/http"
	basicHttp "github.com/taubyte/http/basic"
	basicHttpSecure "github.com/taubyte/http/basic/secure"
	"github.com/taubyte/http/options"
	"github.com/taubyte/p2p/peer"
)

type ConfigHandler interface {
	AutoHttp(node *peer.Node, ops ...options.Option) (http service.Service, err error)
	BasicHttp(ctx context.Context, ops ...options.Option) (http service.Service, err error)
}

type config struct {
	common.GenericConfig
}

// TODO: Change to New(opts...) and takes an option to pass in a config
// TODO: Fix when all other repo's change to github specs
func Configure(genericConfig *common.GenericConfig) ConfigHandler {
	return &config{*genericConfig}
}

func (config *config) AutoHttp(node *peer.Node, ops ...options.Option) (http service.Service, err error) {
	ops = append(ops, options.Listen(config.HttpListen))

	if config.DevMode {
		return config.devHttp(node.Context(), ops...)
	} else {
		http, err = New(node, config.ClientNode, ops...)
		if err != nil {
			return nil, fmt.Errorf("failed https new with client node with: %s", err)
		}

	}

	return
}

func (config *config) BasicHttp(ctx context.Context, ops ...options.Option) (http service.Service, err error) {
	ops = append(ops, options.Listen(config.HttpListen))

	if config.DevMode {
		return config.devHttp(ctx, ops...)
	} else {
		ops = append(ops, options.LoadCertificate(config.TLS.Certificate, config.TLS.Key))
		http, err = basicHttpSecure.New(ctx, ops...)
		if err != nil {
			return nil, fmt.Errorf("failed https new with error: %w", err)
		}
	}

	return
}

func (config *config) devHttp(ctx context.Context, ops ...options.Option) (http service.Service, err error) {
	if !config.HttpSecure {
		http, err = basicHttp.New(ctx, ops...)
	} else {
		ops = append(ops, options.SelfSignedCertificate())

		http, err = basicHttpSecure.New(ctx, ops...)
	}
	if err != nil {
		return nil, fmt.Errorf("failed https new with error: %w", err)
	}

	return
}
