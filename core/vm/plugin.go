package vm

type PluginInstance interface {
	// Load will load all Factories to the HostModule, and return the ModuleInstance
	Load(HostModule) (ModuleInstance, error)

	// Close will close the PluginInstance
	Close() error
}

type Factory interface {
	// Load will initialize the Factory
	Load(hm HostModule) error

	// Close will close and cleanup the Factory
	Close() error

	// Name returns the name of the Factory
	Name() string
}

// TODO: New takes options for factories
type Plugin interface {
	// New creates a new PluginInstance
	New(Instance) (PluginInstance, error)

	// Name returns the name of the Plugin
	Name() string

	Close() error
}
