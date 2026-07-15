package vm

import (
	"io"

	"context"

	api "github.com/samyfodil/wazy/api"
	"github.com/spf13/afero"
)

type Instance interface {
	// Context returns the context of the function Instance
	Context() Context

	// Close will close the Instance
	Close() error

	// Runtime returns a new Function Instance Runtime
	Runtime(*HostModuleDefinitions) (Runtime, error)

	// Filesystem returns the filesystem used by the given Instance.
	Filesystem() afero.Fs

	// Stdout returns the Reader interface of stdout
	Stdout() io.Reader

	// Stderr returns the Reader interface of stderr
	Stderr() io.Reader
}

type Runtime interface {
	Modules() []string
	Module(name string) (ModuleInstance, error)
	Expose(name string) (HostModule, error)
	Attach(plugin Plugin) (PluginInstance, ModuleInstance, error)
	Stdout() io.Reader
	Stderr() io.Reader
	// TODO: Add Runtime Stat
	Close() error
}

// FunctionDefinition is a WebAssembly function exported in a module.
type FunctionDefinition interface {
	// Name is the module-defined name of the function, which is not necessarily
	// the same as its export name.
	Name() string

	// ParamTypes are the possibly empty sequence of value types accepted by a
	// function with this signature.
	ParamTypes() []ValueType

	// ResultTypes are the results of the function.
	ResultTypes() []ValueType
}

// Function and Global are wazy's, exposed directly (wazy is the only engine).
type (
	Function = api.Function
	Global   = api.Global
)

type ModuleInstance interface {
	// Function returns a FunctionInstance of given name from the ModuleInstance
	Function(name string) (FunctionInstance, error)
	Memory() Memory
}

// FunctionInstance is a callable wasm export. It stays a tau interface (rather
// than aliasing api.Function) so it can be mocked in substrate tests without a
// real engine. Params/results are raw wasm uint64 slots — the caller marshals.
type FunctionInstance interface {
	RawCall(ctx context.Context, args ...uint64) ([]uint64, error)
}
