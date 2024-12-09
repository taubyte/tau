package status

import (
	"os"

	"github.com/jedib0t/go-pretty/v6/table"
	client "github.com/taubyte/tau/clients/http/dream"
	"github.com/taubyte/tau/tools/dream/cli/command"
	"github.com/taubyte/tau/tools/dream/cli/common"
	"github.com/urfave/cli/v2"
)

func universe(multiverse *client.Client) *cli.Command {
	c := &cli.Command{
		Name:    "universe",
		Aliases: []string{"u"},
		Action:  universeStatus(multiverse),
	}
	command.NameWithDefault(c, common.DefaultUniverseName)

	return c
}

func universeStatus(multiverse *client.Client) cli.ActionFunc {
	return func(c *cli.Context) (err error) {
		chart, err := multiverse.Universe(c.String("name")).Chart()
		if err != nil {
			return
		}
		rowConfigAutoMerge := table.RowConfig{AutoMerge: true}
		t := table.NewWriter()
		t.SetOutputMirror(os.Stdout)
		t.SetColumnConfigs([]table.ColumnConfig{
			{Number: 1, AutoMerge: true},
			{Number: 2, AutoMerge: true},
		})

		for _, cat := range chart.Nodes {
			for protocol, port := range cat.Value {
				t.AppendRow(table.Row{"Nodes", cat.Name, protocol, port}, rowConfigAutoMerge)
				t.AppendSeparator()

			}
		}
		t.SetStyle(table.StyleLight)
		t.Render()

		return
	}
}
