package jobs

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ipfs/go-log/v2"
)

var (
	logger               = log.Logger("tau.monkey.jobs.client")
	ErrorContextCanceled = errors.New("context cancel")
)

func (c *Context) Run(ctx context.Context) (err error) {
	defer c.handleLog(c.Job.Id)

	c.ctx, c.ctxC = context.WithCancel(ctx)

	go c.startTimeout(ctx, c.ctxC)
	defer c.ctxC()

	if c.Job.Delay != nil {
		select {
		case <-c.ctx.Done():
			return ErrorContextCanceled
		case <-time.After(time.Duration(c.Job.Delay.Time) * time.Second):
		}
	}

	if err = c.cloneAndSet(); err != nil {
		return err
	}

	contextHandler, err := c.Handler()
	if err != nil {
		return err
	}

	err = contextHandler.handle()
	if err != nil {
		fmt.Fprintf(c.LogFile, "Error handling job: %s\n", err)
	}

	return err
}
