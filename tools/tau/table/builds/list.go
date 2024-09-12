package buildsTable

import (
	"os"
	"sort"
	"time"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/taubyte/tau/core/services/patrick"
	authClient "github.com/taubyte/tau/tools/tau/singletons/auth_client"
)

func ListNoRender(jobs []*patrick.Job, showCommit bool) (table.Writer, error) {
	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)
	t.SetAllowedRowLength(79)
	lastColumn := "Job ID"
	if showCommit {
		lastColumn = "Commit"
	}

	_jobs := jobArray(jobs)
	sort.Sort(_jobs)

	t.SetColumnConfigs([]table.ColumnConfig{
		{Align: text.AlignCenter},
		{Name: "Time"},
		{Name: "Type"},
		{Name: lastColumn},
	})

	t.AppendHeader(table.Row{"", "Time", "Type", lastColumn})

	auth, err := authClient.Load()
	if err != nil {
		return nil, err
	}

	timeZone, _ := time.LoadLocation("Local")
	for _, job := range _jobs {
		row, err := row(auth, job, timeZone, showCommit)
		if err != nil {
			return nil, err
		}

		t.AppendRow(row)
		t.AppendSeparator()
	}

	return t, nil
}
