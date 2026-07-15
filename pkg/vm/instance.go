package vm

import (
	"fmt"
	"io"

	wazy "github.com/samyfodil/wazy"
	api "github.com/samyfodil/wazy/api"
	wasi "github.com/samyfodil/wazy/imports/wasi_snapshot_preview1"
	"github.com/spf13/afero"
	"github.com/taubyte/tau/core/vm"
)

var _ vm.Instance = &instance{}

func (i *instance) Runtime(register ...func(wazy.HostModuleBuilder)) (vm.Runtime, error) {
	rt := NewRuntime(i.ctx.Context(), i.config)
	r := &runtime{
		instance:      i,
		modules:       make(map[string]api.Module),
		wasiStartDone: make(chan bool),
		runtime:       rt,
	}

	hm, err := r.Expose("env")
	if err != nil {
		return nil, fmt.Errorf("exposing `env` failed with: %w", err)
	}

	r.registerDefaults(hm.Builder())
	for _, reg := range register {
		reg(hm.Builder())
	}

	if _, err = hm.Compile(); err != nil {
		return nil, fmt.Errorf("compiling host module failed with: %s", err)
	}

	if _, err = wasi.NewBuilder(r.runtime).Instantiate(r.instance.ctx.Context()); err != nil {
		return nil, fmt.Errorf("instantiating host module failed with: %s", err)
	}

	return r, nil
}

func (i *instance) Stdout() io.Reader {
	return i.output
}

func (i *instance) Stderr() io.Reader {
	return i.outputErr
}

func (i *instance) Filesystem() afero.Fs {
	return i.fs
}

func (i *instance) Close() error {
	i.lock.Lock()
	defer i.lock.Unlock()
	i.output.Close()
	i.outputErr.Close()
	return nil
}

func (i *instance) Context() vm.Context {
	return i.ctx
}
