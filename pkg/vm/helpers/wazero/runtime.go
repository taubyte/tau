package helpers

import (
	"context"
	"sync"

	"github.com/taubyte/tau/core/vm"
	"github.com/tetratelabs/wazero"
)

var lock sync.Mutex

func NewRuntime(ctx context.Context, config *vm.Config) wazero.Runtime {
	lock.Lock()
	defer lock.Unlock()
	if config.MemoryLimitPages == 0 {
		config.MemoryLimitPages = vm.MemoryLimitPages
	}

	return wazero.NewRuntimeWithConfig(
		ctx,
		wazero.NewRuntimeConfig().
			WithCloseOnContextDone(true).
			WithDebugInfoEnabled(true).
			WithMemoryLimitPages(config.MemoryLimitPages).
			WithCompilationCache(cache),
	)
}
