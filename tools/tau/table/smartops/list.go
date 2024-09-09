package smartopsTable

import (
	"os"

	"github.com/jedib0t/go-pretty/v6/table"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
)

func List(smartops []*structureSpec.SmartOp) {
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
	for _, smartops := range smartops {
		id := smartops.Id
		if len(smartops.Id) >= 12 {
			id = id[:6] + "..." + id[len(id)-6:]
		}

		t.AppendRow(table.Row{id, smartops.Name, smartops.Call})
		t.AppendSeparator()
	}
	t.SetStyle(table.StyleLight)
	t.Render()
}
