package prompt

import (
	"errors"
	"fmt"
	"os"

	"github.com/jedib0t/go-pretty/v6/table"

	goPrompt "github.com/c-bata/go-prompt"
	list "github.com/taubyte/tau/tools/taucorder/helpers"
)

var monkeyTree = &tctree{
	leafs: []*leaf{
		exitLeaf,
		{
			validator: stringValidator("list"),
			ret: []goPrompt.Suggest{
				{
					Text:        "list",
					Description: "show all jobs that monkey has",
				},
			},
			handler: listMonkeyJobs,
		},
		{
			validator: stringValidator("get"),
			ret: []goPrompt.Suggest{
				{
					Text:        "get",
					Description: "show data about a job",
				},
			},
			handler: getMonkeyJobs,
		},
	},
}

func listMonkeyJobs(p Prompt, args []string) error {
	ids, err := p.MonkeyClient().List()
	if err != nil {
		return fmt.Errorf("failed listing jobs cids with error: %w", err)
	}

	if len(ids) == 0 {
		fmt.Println("No jobs are currently running in monkey")
		return nil
	}

	list.CreateTableIds(ids, "Monkey job Id's")

	return nil
}

func getMonkeyJobs(p Prompt, args []string) error {
	t := table.NewWriter()
	t.SetStyle(table.StyleLight)
	t.SetOutputMirror(os.Stdout)
	if len(args) < 2 {
		fmt.Println("Must provide job ID")
		return errors.New("must provide job ID")
	}
	jid := args[1]
	resp, err := p.MonkeyClient().Status(jid)
	if err != nil {
		return fmt.Errorf("failed listing jobs cids with error: %w", err)
	}

	t.AppendRows([]table.Row{{"Jobs", resp.Jid}}, table.RowConfig{})
	t.AppendRows([]table.Row{{"LogCID", resp.Logs}}, table.RowConfig{})
	t.AppendRows([]table.Row{{"Status", resp.Status.String()}}, table.RowConfig{})

	t.Render()
	return nil
}
