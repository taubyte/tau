package projectLib

import (
	"github.com/taubyte/tau/tools/tau/session"
	"github.com/urfave/cli/v2"
)

func Select(ctx *cli.Context, name string) error {
	session.Unset().SelectedApplication()
	return session.Set().SelectedProject(name)
}

func Deselect(ctx *cli.Context, name string) error {
	return session.Unset().SelectedProject()
}
