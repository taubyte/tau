package app

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
)

// TODO: add hoarder to config when its fixed
// TODO: Build in tmp
func TestStart(t *testing.T) {
	app := App()
	defer os.RemoveAll(shape + odo.ClientPrefix)
	defer os.RemoveAll(shape)

	ctx, ctxC := context.WithTimeout(context.Background(), time.Second*15)
	defer ctxC()

	err := app.RunContext(ctx, append(
		os.Args[0:1],
		[]string{"start", "-s", "test", "-c", "testConfig.yaml", "--dev"}...),
	)
	assert.NilError(t, err)

}
