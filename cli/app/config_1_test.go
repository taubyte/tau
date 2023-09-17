package app

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/taubyte/tau/config"
	"gotest.tools/v3/assert"
)

func TestConfig(t *testing.T) {
	t.Run("GenerateConfig", func(t *testing.T) {
		ctx, ctxC := context.WithTimeout(context.Background(), time.Second*15)
		defer ctxC()

		root := t.TempDir()

		fmt.Println("ROOT ", root)

		err := app.RunContext(ctx, []string{os.Args[0], "cnf", "gen", "-s", "test", "--root", root, "--protos", "auth,seer,monkey", "--swarm-key", "--dv-keys"})
		assert.NilError(t, err)
	})

	t.Run("ValidateConfig", func(t *testing.T) {
		ctx, ctxC := context.WithTimeout(context.Background(), time.Second*15)
		defer ctxC()

		root := t.TempDir()

		err := app.RunContext(ctx, []string{os.Args[0], "cnf", "ok?", "-s", "test", "--root", root})
		assert.NilError(t, err)
	})

	t.Run("ShowConfig", func(t *testing.T) {
		ctx, ctxC := context.WithTimeout(context.Background(), time.Second*15)
		defer ctxC()

		root := t.TempDir()

		err := app.RunContext(ctx, []string{os.Args[0], "cnf", "show", "-s", "test", "--root", root})
		assert.NilError(t, err)
	})

	t.Run("ShowConfigWithDefaultRoot", func(t *testing.T) {
		ctx, ctxC := context.WithTimeout(context.Background(), time.Second*15)
		defer ctxC()

		root := t.TempDir()
		config.DefaultRoot = root

		err := app.RunContext(ctx, []string{os.Args[0], "cnf", "show", "-s", "test"})
		assert.NilError(t, err)
	})
}