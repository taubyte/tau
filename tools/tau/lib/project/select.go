package projectLib

import (
	"github.com/taubyte/tau/tools/tau/env"
	"github.com/taubyte/tau/tools/tau/singletons/session"
	"github.com/urfave/cli/v2"
)

func Select(ctx *cli.Context, name string) error {
	return env.SetSelectedProject(ctx, name)
}

func Deselect(ctx *cli.Context, name string) error {
	return session.Unset().SelectedProject()
}
