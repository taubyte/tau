package vm

import (
	"context"

	"github.com/samyfodil/wazy"
	"github.com/taubyte/tau/core/vm"
)

func NewRuntime(ctx context.Context, config *vm.Config) wazy.Runtime {
	if config == nil {
		config = &vm.Config{}
	}

	if config.MemoryLimitPages == 0 {
		config.MemoryLimitPages = vm.MemoryLimitPages
	}

	return wazy.NewRuntimeWithConfig(
		ctx,
		wazy.NewRuntimeConfig().
			WithCloseOnContextDone(true).
			// DWARF parsing slows every module compile and bloats memory; wasm error
			// stack traces keep function names without it.
			WithDebugInfoEnabled(false).
			WithMemoryLimitPages(config.MemoryLimitPages).
			WithCompilationCache(Cache),
	)
}
