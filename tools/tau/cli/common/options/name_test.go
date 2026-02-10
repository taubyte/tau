package options_test

import (
	"testing"

	"github.com/taubyte/tau/tools/tau/cli/common/options"
	"github.com/taubyte/tau/tools/tau/flags"
	"github.com/urfave/cli/v2"
	"gotest.tools/v3/assert"
)

func TestSetNameAsArgs0(t *testing.T) {
	app := &cli.App{
		Commands: []*cli.Command{
			{
				Name:   "cmd",
				Flags:  []cli.Flag{flags.Name},
				Before: options.SetNameAsArgs0,
				Action: func(ctx *cli.Context) error {
					assert.Equal(t, ctx.String(flags.Name.Name), "myname")
					return nil
				},
			},
		},
	}
	err := app.Run([]string{"tau", "cmd", "myname"})
	assert.NilError(t, err)
}

func TestSetNameAsArgs0_EmptyArgs(t *testing.T) {
	app := &cli.App{
		Commands: []*cli.Command{
			{
				Name:   "cmd",
				Flags:  []cli.Flag{flags.Name},
				Before: options.SetNameAsArgs0,
				Action: func(ctx *cli.Context) error {
					// When no args, name is not set
					return nil
				},
			},
		},
	}
	err := app.Run([]string{"tau", "cmd"})
	assert.NilError(t, err)
}

func TestSetFlagAsArgs0(t *testing.T) {
	app := &cli.App{
		Commands: []*cli.Command{
			{
				Name:   "cmd",
				Flags:  []cli.Flag{flags.Name},
				Before: options.SetFlagAsArgs0(flags.Name.Name),
				Action: func(ctx *cli.Context) error {
					assert.Equal(t, ctx.String(flags.Name.Name), "arg0")
					return nil
				},
			},
		},
	}
	err := app.Run([]string{"tau", "cmd", "arg0"})
	assert.NilError(t, err)
}
