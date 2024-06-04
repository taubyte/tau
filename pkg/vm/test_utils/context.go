package test_utils

import (
	gocontext "context"

	"github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/pkg/vm/context"
)

func Context() (vm.Context, error) {
	return context.New(gocontext.Background(), ContextOptions...)
}
