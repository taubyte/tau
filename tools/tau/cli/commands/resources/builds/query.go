package builds

import (
	"time"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/taubyte/tau/core/services/patrick"
	"github.com/taubyte/tau/tools/tau/cli/common"
	projectLib "github.com/taubyte/tau/tools/tau/lib/project"
	patrickClient "github.com/taubyte/tau/tools/tau/singletons/patrick_client"
	buildsTable "github.com/taubyte/tau/tools/tau/table/builds"
	"github.com/urfave/cli/v2"
)

func (link) Query() common.Command {
	return common.Create(
		&cli.Command{
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:        "since",
					Aliases:     []string{"t", "s"},
					Usage:       "(optional) filters jobs by time range",
					DefaultText: defaultTimeFilter,
				},
			},
			Action: query,
		},
	)
}

func (l link) List() common.Command {
	return l.Query()
}

func query(ctx *cli.Context) error {
	prj, err := projectLib.SelectedProjectInterface()
	if err != nil {
		return err
	}

	patrickC, err := patrickClient.Load()
	if err != nil {
		return err
	}

	jobIds, err := patrickC.Jobs(prj.Get().Id())
	if err != nil {
		// use i18n
		return err
	}

	since := defaultTimeFilter
	if _since := ctx.String("since"); len(_since) > 0 {
		since = _since
	}

	sinceParsed, err := time.ParseDuration(since)
	if err != nil {
		return err
	}

	rangeEnd := time.Now().Add(-sinceParsed).Unix()

	// index string for unique jobs
	jobs := make([]*patrick.Job, 0, len(jobIds))
	for _, id := range jobIds {
		job, err := patrickC.Job(id)
		if err != nil {
			// use i18n
			return err
		}

		if job.Timestamp >= rangeEnd {
			jobs = append(jobs, job)
		}
	}

	// separate keys from original for loop to ensure unique values
	t, err := buildsTable.ListNoRender(jobs, false)
	if err != nil {
		return err
	}

	t.SetStyle(table.StyleLight)
	t.Render()

	return nil
}
