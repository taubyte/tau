package context

import (
	gocontext "context"

	"github.com/taubyte/tau/core/vm"
	spec "github.com/taubyte/tau/pkg/specs/common"
)

func New(ctx gocontext.Context, options ...Option) (vm.Context, error) {
	c := &vmContext{}
	c.ctx, c.ctxC = gocontext.WithCancel(ctx)

	for _, opt := range options {
		if err := opt(c); err != nil {
			return nil, err
		}
	}

	if len(c.branches) == 0 {
		c.branches = spec.DefaultBranches
	}

	return c, nil
}
