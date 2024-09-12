package version

import (
	"github.com/pterm/pterm"
	"github.com/taubyte/tau/tools/tau/common"
	"github.com/urfave/cli/v2"
)

var Command = &cli.Command{
	Name:   "version",
	Action: Run,
}

func Run(ctx *cli.Context) error {
	pterm.Info.Println(common.Version)
	return nil
}
