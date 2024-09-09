package functionTable

import (
	"os"

	"github.com/jedib0t/go-pretty/v6/table"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
)

func List(functions []*structureSpec.Function) {
	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)
	t.SetAllowedRowLength(79)

	colConfigs := make([]table.ColumnConfig, 0)
	colConfigs = append(colConfigs, table.ColumnConfig{
		Name: "ID",
	})
	colConfigs = append(colConfigs, table.ColumnConfig{
		Name: "Name",
	})
	colConfigs = append(colConfigs, table.ColumnConfig{
		Name:     "Call",
		WidthMax: 40,
	})

	t.SetColumnConfigs(colConfigs)
	t.AppendHeader(table.Row{"ID", "Name", "Call"})
	for _, function := range functions {
		id := function.Id
		if len(function.Id) >= 12 {
			id = id[:6] + "..." + id[len(id)-6:]
		}

		t.AppendRow(table.Row{id, function.Name, function.Call})
		t.AppendSeparator()
	}
	t.SetStyle(table.StyleLight)
	t.Render()
}
