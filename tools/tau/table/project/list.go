package projectTable

import (
	"os"

	"github.com/jedib0t/go-pretty/v6/table"
	client "github.com/taubyte/tau/clients/http/auth"
)

// Takes a project and returns a description
type Descriptor func(project *client.Project) string

func List(projects []*client.Project, descriptor Descriptor) {
	ListNoRender(projects, descriptor).Render()
}

func ListNoRender(projects []*client.Project, descriptor Descriptor) table.Writer {
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
		Name:     "Description",
		WidthMax: 40,
	})

	t.SetColumnConfigs(colConfigs)
	t.AppendHeader(table.Row{"ID", "Name", "Description"})
	for _, project := range projects {
		id := project.Id
		if len(project.Id) >= 12 {
			id = id[:6] + "..." + id[len(id)-6:]
		}

		t.AppendRow(table.Row{id, project.Name, descriptor(project)})
		t.AppendSeparator()
	}
	t.SetStyle(table.StyleLight)

	return t
}
