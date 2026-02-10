package projectFlags

import (
	"testing"

	"github.com/urfave/cli/v2"
	"gotest.tools/v3/assert"
)

func TestPrivateFlag(t *testing.T) {
	assert.Assert(t, Private != nil)
	assert.Equal(t, Private.Name, "private")
}

func TestPublicFlag(t *testing.T) {
	assert.Assert(t, Public != nil)
	assert.Equal(t, Public.Name, "public")
}

func TestLocFlag(t *testing.T) {
	assert.Assert(t, Loc != nil)
	assert.Equal(t, Loc.Name, "location")
}

func TestPrivateFlagSet(t *testing.T) {
	app := &cli.App{
		Flags: []cli.Flag{Private, Public},
		Action: func(ctx *cli.Context) error {
			assert.Equal(t, ctx.Bool("private"), true)
			return nil
		},
	}
	err := app.Run([]string{"app", "--private"})
	assert.NilError(t, err)
}

func TestLocFlagWithValue(t *testing.T) {
	app := &cli.App{
		Flags: []cli.Flag{Loc},
		Action: func(ctx *cli.Context) error {
			assert.Equal(t, ctx.String("location"), "/path/to/project")
			return nil
		},
	}
	err := app.Run([]string{"app", "--location", "/path/to/project"})
	assert.NilError(t, err)
}

func TestParseConfigCodeFlags(t *testing.T) {
	t.Run("neither set returns both true", func(t *testing.T) {
		app := &cli.App{
			Flags: []cli.Flag{ConfigOnly, CodeOnly},
			Action: func(ctx *cli.Context) error {
				config, code, err := ParseConfigCodeFlags(ctx)
				assert.NilError(t, err)
				assert.Assert(t, config)
				assert.Assert(t, code)
				return nil
			},
		}
		err := app.Run([]string{"app"})
		assert.NilError(t, err)
	})
	t.Run("both set returns error", func(t *testing.T) {
		app := &cli.App{
			Flags: []cli.Flag{ConfigOnly, CodeOnly},
			Action: func(ctx *cli.Context) error {
				config, code, err := ParseConfigCodeFlags(ctx)
				assert.Assert(t, err != nil)
				assert.Assert(t, !config)
				assert.Assert(t, !code)
				return nil
			},
		}
		err := app.Run([]string{"app", "--config-only", "--code-only"})
		assert.NilError(t, err)
	})
}
