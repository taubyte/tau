package vm

import "context"

// Module return functions exported in a module, post-instantiation.
type Module interface {
	// Name is the name this module was instantiated with. Exported functions can be imported with this name.
	Name() string

	// Memory returns a memory defined in this module or nil if there are none wasn't.
	Memory() Memory

	// ExportedFunction returns a function exported from this module or nil if it wasn't.
	ExportedFunction(name string) Function

	// ExportedMemory returns a memory exported from this module or nil if it wasn't.
	ExportedMemory(name string) Memory

	// ExportedGlobal a global exported from this module or nil if it wasn't.
	ExportedGlobal(name string) Global

	// CloseWithExitCode releases resources allocated for this Module. Use a non-zero exitCode parameter to indicate a
	// failure to ExportedFunction callers. When the context is nil, it defaults to context.Background.
	CloseWithExitCode(exitCode uint32) error

	// Close closes the resource. When the context is nil, it defaults to context.Background.
	Close(context.Context) error
}
