package starlark

import (
	"fmt"
	"io/fs"

	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
	"go.starlark.net/syntax"
)

func makeLoadFunc(v *vm) func(thread *starlark.Thread, module string) (starlark.StringDict, error) {
	return func(thread *starlark.Thread, module string) (starlark.StringDict, error) {
		v.lock.RLock()
		defer v.lock.RUnlock()

		for name, dict := range v.builtins {
			if module == name+".star" {
				return dict, nil
			}
		}

		// nothing builtin, check file system
		var lastErr error
		for _, fsys := range v.filesystems {
			script, err := fs.ReadFile(fsys, module)
			if err != nil {
				lastErr = err
				continue // Try next file system
			}
			predeclared := starlark.StringDict{
				"struct": starlark.NewBuiltin("struct", starlarkstruct.Make),
			}
			opts := syntax.FileOptions{}
			return starlark.ExecFileOptions(&opts, thread, module, script, predeclared)
		}

		return nil, fmt.Errorf("failed to load module %s: %w", module, lastErr) // No file system had the module
	}
}
