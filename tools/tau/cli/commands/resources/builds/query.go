package builds

import (
	"sync"
	"time"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/taubyte/tau/core/services/patrick"
	"github.com/taubyte/tau/tools/tau/cli/common"
	patrickClient "github.com/taubyte/tau/tools/tau/clients/patrick_client"
	"github.com/taubyte/tau/tools/tau/output"
	buildsTable "github.com/taubyte/tau/tools/tau/table/builds"
	"github.com/taubyte/tau/tools/tau/tcc"
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
	store, err := tcc.Open()
	if err != nil {
		return err
	}

	projectID, err := store.ProjectID()
	if err != nil {
		return err
	}

	patrickC, err := patrickClient.Load()
	if err != nil {
		return err
	}

	jobIds, err := patrickC.Jobs(projectID)
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

	// a project's job list only grows, and each job is its own request, so
	// fetch them several at a time; results keep the list's order
	fetched := make([]*patrick.Job, len(jobIds))
	errs := make([]error, len(jobIds))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for i, id := range jobIds {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			fetched[i], errs[i] = patrickC.Job(id)
		}()
	}
	wg.Wait()

	jobs := make([]*patrick.Job, 0, len(jobIds))
	for i, job := range fetched {
		if errs[i] != nil {
			// use i18n
			return errs[i]
		}

		if job.Timestamp >= rangeEnd {
			jobs = append(jobs, job)
		}
	}

	if output.Render(jobs) {
		return nil
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
