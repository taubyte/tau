package jobs

import (
	"context"
	"strings"
	"time"

	protocolCommon "github.com/taubyte/tau/services/common"
)

// Used in tests for setting the unexported contexts
func (c *Context) ForceContext(ctx context.Context) {
	c.ctx, c.ctxC = context.WithCancel(ctx)
	if c.Node != nil && c.ClientNode == nil {
		c.ClientNode = c.Node
	}
}

func (c *Context) Context(ctx context.Context) {
	c.ctx, c.ctxC = context.WithCancel(ctx)
}

func (c *Context) ForceGitDir(dir string) {
	c.gitDir = dir
}

func (c *Context) startTimeout() {
	defaultJobTimeout := protocolCommon.DefaultMaxTime
	if protocolCommon.TimeoutTest {
		defaultJobTimeout = 5 * time.Second
	}

	select {
	case <-c.ctx.Done():
		return
	case <-time.After(defaultJobTimeout):
		err := c.Patrick.Timeout(c.Job.Id)
		if err != nil && !strings.Contains(err.Error(), "finished") {
			logger.Errorf("Sending timeout for job %s failed with %s", c.Job.Id, err.Error())
			return
		}
		c.ctxC()
	}

}
