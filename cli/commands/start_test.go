package commands

import (
	"context"
	"os"
	"testing"
	"time"

	odo "github.com/taubyte/odo/cli"
	"gotest.tools/v3/assert"
)

var (
	shape = "test"
	ports = []int{8100, 8102, 8104}
)

func TestStart(t *testing.T) {
	app, err := Build()
	assert.NilError(t, err)

	defer os.RemoveAll(shape + odo.ClientPrefix)
	defer os.RemoveAll(shape)

	ctx, ctxC := context.WithTimeout(context.Background(), time.Second*2)
	defer ctxC()

	err = app.RunContext(ctx, append(
		os.Args[0:1],
		[]string{"start", "-s", "test", "-c", "testConfig.yaml"}...),
	)
	assert.NilError(t, err)
}
