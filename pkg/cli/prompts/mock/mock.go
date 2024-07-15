package mock

import (
	"os"

	"github.com/urfave/cli/v2"
)

type CLI struct {
	Flags  []cli.Flag
	Action cli.ActionFunc
	ToSet  map[string]string
}

func (m CLI) Run(args ...string) (_ctx *cli.Context, err error) {
	app := &cli.App{
		Flags: m.Flags,

		// Capture ctx for returning
		Action: func(ctx *cli.Context) error {
			_ctx = ctx
			if m.Action != nil {
				return m.Action(_ctx)
			} else {
				return nil
			}
		},
	}

	err = app.Run(append([]string{os.Args[0]}, args...))
	if err != nil {
		return
	}

	if m.ToSet != nil {
		for name, value := range m.ToSet {
			if len(value) > 0 {
				err = _ctx.Set(name, value)
				if err != nil {
					return
				}
			}
		}
	}

	return
}
