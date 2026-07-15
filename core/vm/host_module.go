package vm

import wazy "github.com/samyfodil/wazy"

// HostModule is a wazy host module under construction. Register host functions
// on its Builder (with wazy.HostFuncN / HostProcN, or WithGoModuleFunction for
// raw signatures), then Compile.
type HostModule interface {
	// Builder returns the underlying wazy host-module builder.
	Builder() wazy.HostModuleBuilder

	// Compile instantiates the registered functions and returns the module.
	Compile() (ModuleInstance, error)
}
