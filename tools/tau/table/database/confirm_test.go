package databaseTable_test

import (
	"os"
	"testing"

	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/tools/tau/flags"
	databaseTable "github.com/taubyte/tau/tools/tau/table/database"
	"github.com/urfave/cli/v2"
	"gotest.tools/v3/assert"
)

func TestConfirm(t *testing.T) {
	runWithYesCtx := func(t *testing.T) *cli.Context {
		t.Helper()
		app := &cli.App{Flags: []cli.Flag{flags.Yes}}
		var ctx *cli.Context
		app.Action = func(c *cli.Context) error {
			ctx = c
			return nil
		}
		err := app.Run(append([]string{os.Args[0]}, "--yes"))
		assert.NilError(t, err)
		return ctx
	}

	t.Run("with_yes_flag", func(t *testing.T) {
		ctx := runWithYesCtx(t)
		db := &structureSpec.Database{
			Name:        "testdb",
			Description: "desc",
			Tags:        []string{"t1"},
			Local:       false,
			Size:        1024,
		}
		ok := databaseTable.Confirm(ctx, db, "Create?")
		assert.Assert(t, ok)
	})

	t.Run("local_display", func(t *testing.T) {
		ctx := runWithYesCtx(t)
		db := &structureSpec.Database{
			Name:  "testdb",
			Local: true,
		}
		ok := databaseTable.Confirm(ctx, db, "Create?")
		assert.Assert(t, ok)
	})
}
