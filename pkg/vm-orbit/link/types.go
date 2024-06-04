package link

import (
	"github.com/hashicorp/go-plugin"
	"github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/pkg/vm-orbit/proto"
)

type link struct {
	plugin.NetRPCUnsupportedPlugin
}

type GRPCPluginClient struct {
	broker *plugin.GRPCBroker
	client proto.PluginClient
}

type module struct {
	proto.UnimplementedModuleServer
	module vm.Module
}

var _ vm.FunctionDefinition = &functionDefinition{}

type functionDefinition struct {
	name string
	args []vm.ValueType
	rets []vm.ValueType
}
