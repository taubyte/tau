package flags

import (
	"errors"
	"testing"

	"github.com/urfave/cli/v2"
	"gotest.tools/v3/assert"
)

var testFlagName = "env"

func newBoolFlag() *BoolWithInverseFlag {
	return &BoolWithInverseFlag{
		BoolFlag: &cli.BoolFlag{
			Name: testFlagName,
		},
	}
}

func TestBoolWithInverse(t *testing.T) {
	app := cli.NewApp()
	app.Flags = Combine(newBoolFlag())
	app.Action = func(ctx *cli.Context) error {
		if !ctx.IsSet(testFlagName) {
			return errors.New("Expected flag to be set")
		}

		if !ctx.Bool(testFlagName) {
			return errors.New("Expected flag to be true")
		}
		return nil
	}
	err := app.Run([]string{"app", "--env"})
	assert.NilError(t, err)

	app.Flags = Combine(newBoolFlag())
	app.Action = func(ctx *cli.Context) error {
		if !ctx.IsSet(testFlagName) {
			return errors.New("Expected flag to be set")
		}

		if ctx.Bool(testFlagName) {
			return errors.New("Expected flag to be false")
		}
		return nil
	}
	err = app.Run([]string{"app", "--no-env"})
	assert.NilError(t, err)

	app.Flags = Combine(newBoolFlag())
	app.Action = func(ctx *cli.Context) error {
		if ctx.IsSet(testFlagName) {
			return errors.New("Expected flag to not be set")
		}

		return nil
	}
	err = app.Run([]string{"app"})
	assert.NilError(t, err)
}
