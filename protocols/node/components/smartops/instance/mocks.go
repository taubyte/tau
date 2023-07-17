package instance

import "context"

func MockInstance(ctx context.Context) *instance {
	i := &instance{}
	i.ctx, i.ctxC = context.WithCancel(ctx)
	return i
}
