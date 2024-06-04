package vm

// HostFunction is the function handler of a HostModuleFunctionDefinition
type HostFunction interface{}

// HostModuleFunctionDefinition is the definition of a Function within a HostModule
type HostModuleFunctionDefinition struct {
	Name    string
	Handler HostFunction
}

// HostModuleGlobalDefinition is Global Value stored within the HostModule
type HostModuleGlobalDefinition struct {
	Name  string
	Value interface{}
}

// HostModuleMemoryDefinition is the memory definition of the Host Module.
type HostModuleMemoryDefinition struct {
	Name  string
	Pages struct {
		Min   uint64
		Max   uint64
		Maxed bool
	}
}

type HostModule interface {
	// Functions adds the function definitions to the HostModule
	Functions(...*HostModuleFunctionDefinition) error

	// Memory adds the memory definitions to the HostModule
	Memories(...*HostModuleMemoryDefinition) error

	// Globals adds the global definitions to the HostModule
	Globals(...*HostModuleGlobalDefinition) error

	// Compile will compile the defined HostModule, and return a ModuleInstance
	Compile() (ModuleInstance, error)
}

type HostModuleDefinitions struct {
	Functions []*HostModuleFunctionDefinition
	Memories  []*HostModuleMemoryDefinition
	Globals   []*HostModuleGlobalDefinition
}
