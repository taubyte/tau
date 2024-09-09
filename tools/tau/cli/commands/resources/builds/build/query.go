package build

import (
	"errors"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/taubyte/tau/core/services/patrick"
	"github.com/taubyte/tau/tools/tau/cli/common"
	patrickClient "github.com/taubyte/tau/tools/tau/singletons/patrick_client"
	buildsTable "github.com/taubyte/tau/tools/tau/table/builds"
	"github.com/urfave/cli/v2"
)

func (link) Query() common.Command {
	return common.Create(
		&cli.Command{
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:    "jid",
					Aliases: []string{"id"},
					Usage:   "job id to query",
				},
			},
			Action: query,
		},
	)
}

func query(ctx *cli.Context) error {
	patrickC, err := patrickClient.Load()
	if err != nil {
		return err
	}

	jobId := ctx.String("jid")
	if len(jobId) < 1 {
		return errors.New("job id not set")
	}

	job, err := patrickC.Job(jobId)
	if err != nil {
		return err
	}

	t, err := buildsTable.ListNoRender([]*patrick.Job{job}, true)
	if err != nil {
		return err
	}

	t.SetStyle(table.StyleLight)
	t.Render()

	return nil
}
