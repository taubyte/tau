package smartOps

import (
	"context"
	"errors"

	"github.com/taubyte/tau/core/services/substrate"
	"github.com/taubyte/tau/core/vm"
)

type plugin struct {
	ctx         context.Context
	ctxC        context.CancelFunc
	smartOpNode substrate.SmartOpsService
}

var _plugin *plugin

type Option func() error

func SmartOpNode(node substrate.SmartOpsService) Option {
	return func() error {
		if _plugin == nil {
			return errors.New("failed SmartOpNode option, plugin is null")
		}
		if node == nil {
			return errors.New("failed SmartOpNode option, node is null")
		}

		_plugin.smartOpNode = node
		return nil
	}
}

func (p *plugin) Name() string {
	return "taubyte/smartops"
}

func (p *plugin) Close() error {
	p.ctxC()
	return nil
}

func Plugin() vm.Plugin {
	return _plugin
}

// First initialize the plugin
func Initialize(ctx context.Context, options ...Option) error {
	if _plugin == nil {
		_plugin = &plugin{}
		_plugin.ctx, _plugin.ctxC = context.WithCancel(ctx)
		for _, opt := range options {
			if err := opt(); err != nil {
				return err
			}
		}

		go func() {
			<-_plugin.ctx.Done()
			_plugin.ctxC()
			_plugin = nil
		}()

	}

	return nil
}
