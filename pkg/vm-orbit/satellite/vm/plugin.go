package vm

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/taubyte/tau/core/vm"
)

// TODO: Handle ma as multi-address
func Load(filename string, ctx context.Context) (vm.Plugin, error) {
	if len(filename) < 1 {
		return nil, errors.New("cannot load plugin from empty filename")
	}

	if _, err := os.Stat(filename); err != nil {
		return nil, fmt.Errorf("stat `%s` failed with: %w", filename, err)
	}

	p := &vmPlugin{
		origin:    filename,
		instances: make(map[*pluginInstance]interface{}),
	}

	if err := p.prepFile(); err != nil {
		return nil, fmt.Errorf("prepping `%s` failed with: %w", p.origin, err)
	}

	if err := p.hashFile(); err != nil {
		return nil, fmt.Errorf("hashing `%s` failed with: %w", p.origin, err)
	}

	p.ctx, p.ctxC = context.WithCancel(ctx)

	p.connect()
	if err := p.watch(); err != nil {
		p.ctxC()
		return nil, fmt.Errorf("watch on file `%s` failed with: %w", p.filename, err)
	}

	return p, nil
}

func (p *vmPlugin) Name() string {
	return p.name
}

func (p *vmPlugin) New(instance vm.Instance) (vm.PluginInstance, error) {
	pI, err := p.new(instance)
	if err != nil {
		return nil, fmt.Errorf("creating new plugin instance for plugin `%s` failed with: %w", p.name, err)
	}

	p.lock.Lock()
	p.instances[pI] = nil
	p.lock.Unlock()

	return pI, nil
}

func (p *vmPlugin) Close() error {
	p.lock.Lock()
	defer p.lock.Unlock()
	var err error
	for pI := range p.instances {
		if _err := pI.close(); _err != nil {
			err = _err
		}
	}

	p.ctxC()
	p.proc.Kill()
	return err
}
