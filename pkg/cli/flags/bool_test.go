package flags

import (
	"errors"
	"testing"

	"github.com/urfave/cli/v2"
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
		if ctx.IsSet(testFlagName) == false {
			return errors.New("Expected flag to be set")
		}

		if ctx.Bool(testFlagName) == false {
			return errors.New("Expected flag to be true")
		}
		return nil
	}
	err := app.Run([]string{"app", "--env"})
	if err != nil {
		t.Error(err)
		return
	}

	app.Flags = Combine(newBoolFlag())
	app.Action = func(ctx *cli.Context) error {
		if ctx.IsSet(testFlagName) == false {
			return errors.New("Expected flag to be set")
		}

		if ctx.Bool(testFlagName) == true {
			return errors.New("Expected flag to be false")
		}
		return nil
	}
	err = app.Run([]string{"app", "--no-env"})
	if err != nil {
		t.Error(err)
		return
	}

	app.Flags = Combine(newBoolFlag())
	app.Action = func(ctx *cli.Context) error {
		if ctx.IsSet(testFlagName) == true {
			return errors.New("Expected flag to not be set")
		}

		return nil
	}
	err = app.Run([]string{"app"})
	if err != nil {
		t.Error(err)
		return
	}
}
