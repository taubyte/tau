package vm

import (
	"io"

	ma "github.com/multiformats/go-multiaddr"
)

type Backend interface {
	// Returns the URI scheme the backend supports.
	Scheme() string
	// Get attempts to retrieve the WASM asset.
	Get(multAddr ma.Multiaddr) (io.ReadCloser, error)
	// Close will close the Backend.
	Close() error
}

type Resolver interface {
	// Lookup resolves a module name and returns the uri
	Lookup(ctx Context, module string) (ma.Multiaddr, error)
}

type Loader interface {
	// Load resolves the module, then loads the module using a Backend
	Load(ctx Context, module string) (io.ReadCloser, error)
}

type Source interface {
	// Module Loads the given module name, and returns the SourceModule
	Module(ctx Context, name string) (SourceModule, error)
}

type SourceModule []byte
