package starlark

import (
	"io/fs"
	"sync"

	"go.starlark.net/starlark"
)

// vm represents a Starlark VM that can load scripts from multiple file systems.
type vm struct {
	lock        sync.RWMutex
	filesystems []fs.FS
	load        func(thread *starlark.Thread, module string) (starlark.StringDict, error)
	builtins    map[string]starlark.StringDict
}

// ctx represents a script execution context within a VM.
type ctx struct {
	thread  *starlark.Thread
	globals starlark.StringDict
}
