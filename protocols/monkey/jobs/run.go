package jobs

import (
	"context"
	"time"

	moody "bitbucket.org/taubyte/go-moody-blues"
	"github.com/pkg/errors"
	ifaecMoody "github.com/taubyte/go-interfaces/moody"
)

var (
	logger ifaecMoody.Logger
)

func init() {
	var err error
	logger, err = moody.New("monkey.jobs.client")
	if err != nil {
		panic(errors.Wrap(err, "Initializing moody logger failed"))
	}
}

func (c *Context) Run(ctx context.Context, ctxC context.CancelFunc) (err error) {
	c.ctx, c.ctxC = ctx, ctxC
	go c.startTimeout(ctx, ctxC)
	defer ctxC()

	if c.Job.Delay != nil {
		<-time.After(time.Duration(c.Job.Delay.Time) * time.Second)
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
