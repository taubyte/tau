package auto

import (
	"context"
	"fmt"

	service "github.com/taubyte/http"
	basicHttp "github.com/taubyte/http/basic"
	basicHttpSecure "github.com/taubyte/http/basic/secure"
	"github.com/taubyte/http/options"
	"github.com/taubyte/odo/config"
	"github.com/taubyte/p2p/peer"
)

type ConfigHandler interface {
	AutoHttp(node peer.Node, ops ...options.Option) (http service.Service, err error)
	BasicHttp(ctx context.Context, ops ...options.Option) (http service.Service, err error)
}

type autoConf struct {
	config.Protocol
}

// TODO: Change to New(opts...) and takes an option to pass in a config
// TODO: Fix when all other repo's change to github specs
func Configure(conf *config.Protocol) ConfigHandler {
	return &autoConf{*conf}
}

func (config *autoConf) AutoHttp(node peer.Node, ops ...options.Option) (http service.Service, err error) {
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

func (config *autoConf) BasicHttp(ctx context.Context, ops ...options.Option) (http service.Service, err error) {
	ops = append(ops, options.Listen(config.HttpListen))

	if config.DevMode {
		return config.devHttp(ctx, ops...)
	} else {
		http, err = basicHttpSecure.New(ctx, ops...)
		if err != nil {
			return nil, fmt.Errorf("failed https new with error: %w", err)
		}
	}

	return
}

func (config *autoConf) devHttp(ctx context.Context, ops ...options.Option) (http service.Service, err error) {
	if !config.EnableHTTPS {
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
