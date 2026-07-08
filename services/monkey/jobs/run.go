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

// Run executes the job body. The service's monkeys[jid] entry is owned by
// monkey.Run: deleting it here (as this used to) removed the entry before the
// final status was even set, so status queries 404'd the moment a job
// finished, and monkey.Run's MockedPatrick-aware cleanup — which keeps
// entries for tests — was dead code.
func (c *Context) Run(ctx context.Context) (err error) {
	defer c.handleLog()

	go c.startTimeout()
	defer c.ctxC()

	if c.Job.Delay != nil {
		select {
		case <-c.ctx.Done():
		case <-ctx.Done():
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
