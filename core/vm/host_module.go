package vm

import "context"

// HostFunction is the function handler of a HostModuleFunctionDefinition.
//
// It is an arbitrary Go func whose wasm signature is derived by reflection
// once at registration. Prefer the reflection-free typed path (Stack +
// ParamTypes/ResultTypes) on hot call paths; see HostModuleFunctionDefinition.
type HostFunction interface{}

// StackHostFunction is a reflection-free host function: it reads its wasm
// parameters from, and writes its results to, stack (little-endian uint64
// slots, one per value, following the wasm numeric encoding). It matches the
// engine's native calling convention, so registering one costs no reflection
// at call time.
type StackHostFunction = func(ctx context.Context, module Module, stack []uint64)

// HostModuleFunctionDefinition is the definition of a Function within a HostModule.
//
// There are two ways to define the implementation:
//
//   - Handler: a typed Go func. Its wasm signature is derived by reflecting
//     the func type once at registration; each call then goes through
//     reflection. Convenient, but not free per call.
//
//   - Stack + ParamTypes + ResultTypes: a StackHostFunction with an explicit
//     wasm signature, registered directly with the engine. No reflection at
//     registration or at call time. Use this on hot paths. When Stack is set,
//     Handler is ignored.
type HostModuleFunctionDefinition struct {
	Name    string
	Handler HostFunction

	Stack       StackHostFunction
	ParamTypes  []ValueType
	ResultTypes []ValueType
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
