package jobs

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/taubyte/tau/core/services/patrick"
	"gotest.tools/v3/assert"
)

func TestRunDelay(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")
	logFile, err := os.Create(logPath)
	assert.NilError(t, err)
	defer logFile.Close()

	u, err := startDreamland(t.Name())
	assert.NilError(t, err)
	defer u.Stop()

	simple, err := u.Simple("client")
	assert.NilError(t, err)

	hoarderClient, err := simple.Hoarder()
	assert.NilError(t, err)

	c := &Context{
		Job: &patrick.Job{
			Delay: &patrick.DelayConfig{
				Time: 300,
			},
			Logs: map[string]string{},
		},
		LogFile: logFile,
		Node:    simple,
		Monkey:  &mockMonkey{hoarder: hoarderClient},
	}

	ctx, ctxC := context.WithTimeout(context.Background(), 1*time.Second)
	defer ctxC()
	err = c.Run(ctx)
	assert.Equal(t, err, ErrorContextCanceled)
}
