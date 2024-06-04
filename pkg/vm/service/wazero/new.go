package service

import (
	"context"

	"github.com/taubyte/tau/core/vm"
)

var _ vm.Service = &service{}

func New(ctx context.Context, source vm.Source) vm.Service {
	s := &service{}
	s.ctx, s.ctxC = context.WithCancel(ctx)
	s.source = source
	return s
}
