package application

import (
	applicationLib "github.com/taubyte/tau/tools/tau/lib/application"
	applicationTable "github.com/taubyte/tau/tools/tau/table/application"
	"github.com/urfave/cli/v2"
)

func list(ctx *cli.Context) error {
	applications, err := applicationLib.ListResources()
	if err != nil {
		return err
	}

	applicationTable.List(applications)
	return nil
}
