package vm

import (
	"io"

	"context"

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

// Function is a WebAssembly function exported from an instantiated module.
type Function interface {
	// Definition is metadata about this function from its defining module.
	Definition() FunctionDefinition

	// Call invokes the function with parameters encoded according to ParamTypes. Up to one result is returned.
	Call(ctx context.Context, params ...uint64) ([]uint64, error)
}

// Global is a WebAssembly 1.0 (20191205) global exported from an instantiated module.
type Global interface {
	// Type describes the numeric type of the global.
	Type() ValueType

	// Get returns the last known value of this global. When the context is nil, it defaults to context.Background.
	Get() uint64
}

// MutableGlobal is a Global whose value can be updated at runtime (variable).
type MutableGlobal interface {
	Global

	// Set updates the value of this global. When the context is nil, it defaults to context.Background.
	Set(v uint64)
}

type ModuleInstance interface {
	// Function returns a FunctionInstance of given name from the ModuleInstance
	Functions() []FunctionDefinition
	Function(name string) (FunctionInstance, error)
	Memory() Memory
}

type FunctionInstanceCommon interface {
	// Cancel will cancel the context of the FunctionInstance
	Cancel() error
}

type FunctionInstance interface {
	FunctionInstanceCommon
	Call(ctx context.Context, args ...interface{}) Return
	RawCall(ctx context.Context, args ...uint64) ([]uint64, error)
}

type Return interface {
	// Returns an error
	Error() error

	// Rets will returns the raw uint64 values of the call return
	Rets() []uint64

	// Reflect assigns the return values to the given args
	Reflect(args ...interface{}) error
}
