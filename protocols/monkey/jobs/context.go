package jobs

import (
	"context"
	"fmt"
	"time"

	moodyCommon "github.com/taubyte/go-interfaces/moody"
	"github.com/taubyte/odo/protocols/monkey/common"
)

// Used in tests for setting the unexported contexts
func (c *Context) ForceContext(ctx context.Context) {
	c.ctx, c.ctxC = context.WithCancel(ctx)
	if c.Node != nil && c.OdoClientNode == nil {
		c.OdoClientNode = c.Node
	}
}

func (c *Context) ForceGitDir(dir string) {
	c.gitDir = dir
}

func (c *Context) startTimeout(ctx context.Context, ctxC context.CancelFunc) {
	defaultWaitTime := common.DefaultLockTime
	if common.TimeoutTest {
		defaultWaitTime = 5
	}

	<-time.After(time.Duration(defaultWaitTime) * time.Second)
	err := c.Patrick.Timeout(c.Job.Id)
	if err != nil {
		common.Logger.Error(moodyCommon.Object{"msg": fmt.Sprintf("Sending timeout for job %s failed with %v", c.Job.Id, err)})
		return
	}

	c.Monkey.Delete(c.Job.Id)
}
