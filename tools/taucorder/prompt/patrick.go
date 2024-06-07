package prompt

import (
	"errors"
	"fmt"
	"os"

	"github.com/jedib0t/go-pretty/v6/table"

	goPrompt "github.com/c-bata/go-prompt"
	list "github.com/taubyte/tau/tools/taucorder/helpers"
)

var patrickTree = &tctree{
	leafs: []*leaf{
		exitLeaf,
		{
			validator: stringValidator("list"),
			ret: []goPrompt.Suggest{
				{
					Text:        "list",
					Description: "show all jobs cids",
				},
			},
			handler: listJobs,
		},
		{
			validator: stringValidator("get"),
			ret: []goPrompt.Suggest{
				{
					Text:        "get",
					Description: "show a job",
				},
			},
			handler: getJobs,
		},
	},
}

func listJobs(p Prompt, args []string) error {
	ids, err := p.TaubytePatrickClient().List()
	if err != nil {
		return fmt.Errorf("failed listing jobs cids with error: %w", err)
	}

	if len(ids) == 0 {
		fmt.Println("No jobs are currently stored")
		return nil
	}
	list.CreateTableIds(ids, "Job Id's")

	return nil
}

func getJobs(p Prompt, args []string) error {
	t := table.NewWriter()
	t.SetStyle(table.StyleLight)
	t.SetOutputMirror(os.Stdout)
	if len(args) < 2 {
		fmt.Println("Must provide job ID")
		return errors.New("must provide job ID")
	}
	jid := args[1]
	job, err := p.TaubytePatrickClient().Get(jid)
	if err != nil {
		t.AppendRows([]table.Row{{"--"}},
			table.RowConfig{})

		return fmt.Errorf("failed listing jobs cids with error: %w", err)
	}
	t.AppendRows([]table.Row{{"Id ", job.Id}},
		table.RowConfig{})
	t.AppendSeparator()
	t.AppendRows([]table.Row{{"TimeStamp ", job.Timestamp}},
		table.RowConfig{})
	t.AppendSeparator()
	t.AppendRows([]table.Row{{"Logs ", job.Logs}},
		table.RowConfig{})
	t.AppendSeparator()
	t.AppendRows([]table.Row{{"Status", job.Status}},
		table.RowConfig{})
	t.AppendSeparator()
	t.AppendRows([]table.Row{{"Meta:\n"}},
		table.RowConfig{})
	t.AppendRows([]table.Row{{"\tBefore", job.Meta.Before + "\n"},
		{"\tAfter ", job.Meta.After + "\n"},
		{"\tHeadCommitId", job.Meta.HeadCommit.ID + "\n"},
		{"\tRef", job.Meta.Ref + "\n"},
		{"\tRepository:", "\n"},
		{"\t\tId", job.Meta.Repository.ID},
		{"\t\tProvider", job.Meta.Repository.Provider},
		{"\t\tShURL", job.Meta.Repository.SSHURL}},
		table.RowConfig{})
	t.AppendSeparator()
	t.AppendRows([]table.Row{{"Attempt", job.Attempt}},
		table.RowConfig{})
	t.AppendSeparator()
	t.Render()
	return nil
}
