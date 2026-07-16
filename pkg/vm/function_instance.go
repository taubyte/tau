package vm

import (
	"context"

	"github.com/taubyte/tau/core/vm"
)

var _ vm.FunctionInstance = &funcInstance{}

func (f *funcInstance) RawCall(ctx context.Context, args ...uint64) ([]uint64, error) {
	return f.function.Call(ctx, args...)
}
