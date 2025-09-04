package taubyte

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/taubyte/tau/core/services/substrate/components/database"
	"github.com/taubyte/tau/core/services/substrate/components/p2p"
	"github.com/taubyte/tau/core/services/substrate/components/pubsub"
	"github.com/taubyte/tau/core/services/substrate/components/storage"
	"github.com/taubyte/tau/core/vm"
)

var (
	_plugin      *plugin
	errNilPlugin = errors.New("plugin is nil, need to initialize")
)

type Option func() error

func PubsubNode(node pubsub.Service) Option {
	return func() (err error) {
		if _plugin == nil {
			return errNilPlugin
		}

		if err = _plugin.setNode(node); err != nil {
			return fmt.Errorf("setting pubsub node failed with: %w", err)
		}

		return
	}
}

func DatabaseNode(node database.Service) Option {
	return func() (err error) {
		if _plugin == nil {
			return errNilPlugin
		}

		if err = _plugin.setNode(node); err != nil {
			return fmt.Errorf("setting database node failed with: %w", err)
		}

		return
	}
}

func StorageNode(node storage.Service) Option {
	return func() (err error) {
		if _plugin == nil {
			return errNilPlugin
		}

		if err = _plugin.setNode(node); err != nil {
			return fmt.Errorf("setting storage node failed with: %w", err)
		}

		return
	}
}

func P2PNode(node p2p.Service) Option {
	return func() (err error) {
		if _plugin == nil {
			return errNilPlugin
		}

		if err = _plugin.setNode(node); err != nil {
			return fmt.Errorf("setting p2p node failed with: %w", err)
		}

		return
	}
}

func (p *plugin) Name() string {
	return "taubyte/sdk"
}

func (p *plugin) Close() error {
	p.ctxC()
	return nil
}

func Plugin() vm.Plugin {
	return _plugin
}

var initializeLock sync.Mutex

// First initialize the plugin
func Initialize(ctx context.Context, options ...Option) error {
	initializeLock.Lock()
	defer initializeLock.Unlock()

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
