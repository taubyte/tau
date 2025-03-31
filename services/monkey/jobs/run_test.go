package jobs

import (
	"context"
	"testing"
	"time"

	"github.com/taubyte/tau/core/services/patrick"
	"gotest.tools/v3/assert"
)

func TestRunDelay(t *testing.T) {
	c := &Context{
		Job: &patrick.Job{
			Delay: &patrick.DelayConfig{
				Time: 300,
			},
		},
	}

	ctx, ctxC := context.WithTimeout(context.Background(), 1*time.Second)
	defer ctxC()
	err := c.Run(ctx)
	assert.Equal(t, err, ErrorContextCanceled)
}
