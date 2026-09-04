package build

import (
	"errors"

	"github.com/urfave/cli/v2"

	"github.com/taubyte/tau/tools/tau/cli/common"
	buildClient "github.com/taubyte/tau/tools/tau/clients/build"
	"github.com/taubyte/tau/tools/tau/output"
	jobsTable "github.com/taubyte/tau/tools/tau/table/jobs"
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
	client, err := buildClient.Load()
	if err != nil {
		return err
	}

	jobID := ctx.String("jid")
	if jobID == "" {
		return errors.New("job id not set")
	}

	job, err := client.Job(ctx.Context, jobID)
	if err != nil {
		return err
	}

	if output.Render(job) {
		return nil
	}

	jobsTable.One(job, nil).Render()
	return nil
}
