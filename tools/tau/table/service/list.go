package serviceTable

import (
	"os"

	"github.com/jedib0t/go-pretty/v6/table"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
)

func List(services []*structureSpec.Service) {
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
		Name:     "Protocol",
		WidthMax: 40,
	})

	t.SetColumnConfigs(colConfigs)
	t.AppendHeader(table.Row{"ID", "Name", "Protocol"})
	for _, service := range services {
		id := service.Id
		if len(service.Id) >= 12 {
			id = id[:6] + "..." + id[len(id)-6:]
		}

		t.AppendRow(table.Row{id, service.Name, service.Protocol})
		t.AppendSeparator()
	}
	t.SetStyle(table.StyleLight)
	t.Render()
}
