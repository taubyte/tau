package build

import (
	"errors"

	"github.com/taubyte/tau/tools/tau/cli/common"
	buildClient "github.com/taubyte/tau/tools/tau/clients/build"
	"github.com/urfave/cli/v2"
)

func (link) Cancel() common.Command {
	return common.Create(
		&cli.Command{
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:    "jid",
					Aliases: []string{"id"},
					Usage:   "job id to cancel",
				},
			},
			Action: cancel,
		},
	)
}

func cancel(ctx *cli.Context) error {
	client, err := buildClient.Load()
	if err != nil {
		return err
	}

	jobId := ctx.String("jid")
	if len(jobId) < 1 {
		return errors.New("job id not set")
	}

	err = client.Cancel(ctx.Context, jobId)
	return err
}
