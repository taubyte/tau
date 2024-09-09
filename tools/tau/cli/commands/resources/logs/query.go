package logs

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path"

	"github.com/taubyte/tau/tools/tau/cli/common"
	patrickClient "github.com/taubyte/tau/tools/tau/singletons/patrick_client"
	"github.com/urfave/cli/v2"
)

func (link) Query() common.Command {
	return common.Create(
		&cli.Command{
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:  "jid",
					Usage: "(required) job id of log to query",
				},
				&cli.StringFlag{
					Name:    "output",
					Aliases: []string{"o"},
					Usage:   "set output dir of log files (defaults to terminal)",
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

	outputDir := ctx.String("output")
	var writeFile bool
	if len(outputDir) > 0 {
		if err = os.MkdirAll(outputDir, 0751); err != nil {
			return err
		}

		writeFile = true
	}

	for resourceId, cid := range job.Logs {
		log, err := patrickC.LogFile(jobId, cid)
		if err != nil {
			return err
		}

		data, err := io.ReadAll(log)
		if err != nil {
			return err
		}

		if writeFile {
			if err = os.WriteFile(path.Join(outputDir, resourceId), data, 0666); err != nil {
				return err
			}
		} else {
			fmt.Printf("-----------------------------------------------------------------------------\nResource: %s\n\n%s\n\n", resourceId, string(data))
		}
	}

	return nil
}
