package vm

import (
	wazy "github.com/samyfodil/wazy"
	"github.com/taubyte/tau/core/vm"
)

var _ vm.HostModule = &hostModule{}

func (hm *hostModule) Builder() wazy.HostModuleBuilder {
	return hm.builder
}

func (hm *hostModule) Compile() (vm.ModuleInstance, error) {
	cm, err := hm.builder.Instantiate(hm.ctx.Context())
	if err != nil {
		return nil, err
	}
	return &moduleInstance{
		module: cm,
		ctx:    hm.ctx.Context(),
	}, nil
}
