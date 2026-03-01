package run

import (
	"github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/pkg/vm-low-orbit/dns"
	"github.com/taubyte/tau/pkg/vm-low-orbit/event"
	"github.com/taubyte/tau/pkg/vm-low-orbit/helpers"
	"github.com/taubyte/tau/pkg/vm-low-orbit/http/client"
	"github.com/taubyte/tau/pkg/vm-low-orbit/self"
)

const pluginName = "taubyte/sdk"

type minimalPlugin struct{}

func (p *minimalPlugin) Name() string {
	return pluginName
}

func (p *minimalPlugin) Close() error {
	return nil
}

func (p *minimalPlugin) New(instance vm.Instance) (vm.PluginInstance, error) {
	h := helpers.New(instance.Context().Context())
	eventFactory := event.New(instance, h)
	return &minimalPluginInstance{
		eventFactory: eventFactory,
		factories: []vm.Factory{
			eventFactory,
			client.New(instance, h),
			self.New(instance, h),
			dns.New(instance, h),
		},
	}, nil
}
