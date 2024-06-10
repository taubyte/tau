package prompt

import (
	"fmt"
	"os"

	goPrompt "github.com/c-bata/go-prompt"
	"github.com/jedib0t/go-pretty/v6/table"
)

var authStatusTree = &tctree{
	leafs: []*leaf{
		exitLeaf,
		{
			validator: stringValidator("db"),
			ret: []goPrompt.Suggest{
				{
					Text:        "db",
					Description: "show database stats",
				},
			},
			handler: func(p Prompt, args []string) error {
				s, err := p.TaubyteAuthClient().Stats().Database()
				if err != nil {
					return err
				}

				if len(s.Heads()) == 0 {
					fmt.Println("Database is empty.")
					return nil
				}

				t := table.NewWriter()
				t.SetStyle(table.StyleLight)
				t.SetOutputMirror(os.Stdout)
				t.SetColumnConfigs([]table.ColumnConfig{
					{
						Number:    1,
						AutoMerge: true,
					},
				})

				for _, hcid := range s.Heads() {
					t.AppendRows([]table.Row{
						{"Heads", hcid.String()},
					}, table.RowConfig{AutoMerge: true})
				}

				t.AppendSeparator()

				t.Render()

				return nil
			},
		},
	},
}
