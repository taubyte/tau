package builds

import (
	"time"

	"github.com/urfave/cli/v2"

	buildv1 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v1"
	"github.com/taubyte/tau/tools/tau/cli/common"
	buildClient "github.com/taubyte/tau/tools/tau/clients/build"
	"github.com/taubyte/tau/tools/tau/output"
	jobsTable "github.com/taubyte/tau/tools/tau/table/jobs"
	"github.com/taubyte/tau/tools/tau/tcc"
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
	store, err := tcc.Open()
	if err != nil {
		return err
	}

	projectID, err := store.ProjectID()
	if err != nil {
		return err
	}

	client, err := buildClient.Load()
	if err != nil {
		return err
	}

	ids, err := client.Jobs(ctx.Context, projectID)
	if err != nil {
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

	jobs := make([]*buildv1.Job, 0, len(ids))
	for _, id := range ids {
		job, err := client.Job(ctx.Context, id)
		if err != nil {
			return err
		}
		if job.GetTimestamp() >= rangeEnd {
			jobs = append(jobs, job)
		}
	}

	if output.Render(jobs) {
		return nil
	}

	jobsTable.List(jobs, nil).Render()
	return nil
}
