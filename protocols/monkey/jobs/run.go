package jobs

import (
	"context"
	"errors"
	"time"

	"github.com/ipfs/go-log/v2"
)

var (
	logger               = log.Logger("tau.monkey.jobs.client")
	ErrorContextCanceled = errors.New("context cancel")
)

func (c *Context) Run(ctx context.Context, ctxC context.CancelFunc) (err error) {
	c.ctx, c.ctxC = ctx, ctxC
	go c.startTimeout(ctx, ctxC)
	defer ctxC()

	if c.Job.Delay != nil {
		select {
		case <-time.After(time.Duration(c.Job.Delay.Time) * time.Second):
		case <-c.ctx.Done():
			return ErrorContextCanceled
		}
	}

	if err = c.cloneAndSet(); err != nil {
		return err
	}

	contextHandler, err := c.Handler()
	if err != nil {
		return err
	}

	return contextHandler.handle()
}
