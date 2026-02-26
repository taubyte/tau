package file

import (
	"path/filepath"

	ma "github.com/multiformats/go-multiaddr"

	"github.com/taubyte/tau/core/vm"
	_ "github.com/taubyte/tau/pkg/vm/resolvers/taubyte"
)

type resolver struct {
	wasmPath string
}

// New returns a vm.Resolver that maps any module name to a file multiaddr for the given WASM path.
// wasmPath is resolved to an absolute path so it works regardless of working directory.
func New(wasmPath string) vm.Resolver {
	abs, err := filepath.Abs(wasmPath)
	if err != nil {
		abs = wasmPath
	}
	return &resolver{wasmPath: abs}
}

func (r *resolver) Lookup(ctx vm.Context, name string) (ma.Multiaddr, error) {
	return ma.NewMultiaddr("/file/" + r.wasmPath)
}
