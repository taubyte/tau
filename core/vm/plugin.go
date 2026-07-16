package vm

import wazy "github.com/samyfodil/wazy"

type PluginInstance interface {
	// Load will load all Factories to the HostModule, and return the ModuleInstance
	Load(HostModule) (ModuleInstance, error)

	// Close will close the PluginInstance
	Close() error
}

type Factory interface {
	// Close will close and cleanup the Factory
	Close() error

	// Name returns the name of the Factory
	Name() string
}

// HostFunctionProvider is implemented by factories that register their host
// functions onto a wazy host-module builder (typed, via wazy.HostFuncN /
// HostProcN). The plugin loader calls this for each factory.
type HostFunctionProvider interface {
	RegisterHostFunctions(wazy.HostModuleBuilder)
}

// TODO: New takes options for factories
type Plugin interface {
	// New creates a new PluginInstance
	New(Instance) (PluginInstance, error)

	// Name returns the name of the Plugin
	Name() string

	Close() error
}
