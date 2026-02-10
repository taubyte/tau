package build

import (
	"errors"

	"github.com/taubyte/tau/tools/tau/cli/common"
	patrickClient "github.com/taubyte/tau/tools/tau/clients/patrick_client"
	"github.com/urfave/cli/v2"
)

func (link) Retry() common.Command {
	return common.Create(
		&cli.Command{
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:    "jid",
					Aliases: []string{"id"},
					Usage:   "job id to retry",
				},
			},
			Action: retry,
		},
	)
}

func retry(ctx *cli.Context) error {
	patrickC, err := patrickClient.Load()
	if err != nil {
		return err
	}

	jobId := ctx.String("jid")
	if len(jobId) < 1 {
		return errors.New("job id not set")
	}

	_, err = patrickC.Retry(jobId)
	return err
}
