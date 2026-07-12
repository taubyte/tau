package helpers

import (
	"context"

	"github.com/taubyte/tau/core/vm"
	"github.com/tetratelabs/wazero"
)

func NewRuntime(ctx context.Context, config *vm.Config) wazero.Runtime {
	if config == nil {
		config = &vm.Config{}
	}

	if config.MemoryLimitPages == 0 {
		config.MemoryLimitPages = vm.MemoryLimitPages
	}

	return wazero.NewRuntimeWithConfig(
		ctx,
		wazero.NewRuntimeConfig().
			WithCloseOnContextDone(true).
			// DWARF parsing slows every module compile and bloats memory; wasm error
			// stack traces keep function names without it.
			WithDebugInfoEnabled(false).
			WithMemoryLimitPages(config.MemoryLimitPages).
			WithCompilationCache(Cache),
	)
}
