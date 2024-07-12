package starlark

import (
	"fmt"
	"io/fs"

	"go.starlark.net/starlark"
)

func New(filesystems ...fs.FS) (VM, error) {
	if len(filesystems) == 0 {
		return nil, fmt.Errorf("no file systems provided")
	}
	v := &vm{
		filesystems: filesystems,
		builtins:    make(map[string]starlark.StringDict),
	}
	v.load = makeLoadFunc(v)
	return v, nil
}

func (v *vm) File(module string) (Context, error) {
	thread := &starlark.Thread{Name: "main", Load: v.load}
	globals, err := v.load(thread, module)
	if err != nil {
		return nil, fmt.Errorf("failed to load module %s: %w", module, err)
	}
	return &ctx{thread: thread, globals: globals}, nil
}
