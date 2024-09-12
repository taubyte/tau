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
			WithDebugInfoEnabled(true).
			WithMemoryLimitPages(config.MemoryLimitPages).
			WithCompilationCache(Cache),
	)
}
