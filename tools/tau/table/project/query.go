package projectTable

import (
	"os"

	"github.com/jedib0t/go-pretty/v6/table"
	client "github.com/taubyte/tau/clients/http/auth"
	projectLib "github.com/taubyte/tau/tools/tau/lib/project"
)

func Query(project *client.Project, repos *client.RawRepoDataOuter, description string) {
	t := table.NewWriter()

	colConfigs := make([]table.ColumnConfig, 0)
	colConfigs = append(colConfigs, table.ColumnConfig{
		Number:   2,
		WidthMax: 55,
	})
	t.SetColumnConfigs(colConfigs)
	t.SetOutputMirror(os.Stdout)

	// Basic information
	t.AppendRow(table.Row{"ID", project.Id})
	t.AppendSeparator()
	t.AppendRow(table.Row{"Name", project.Name})
	t.AppendSeparator()
	t.AppendRow(table.Row{"Description", description})
	t.AppendSeparator()

	// Repository information
	t.AppendRow(table.Row{"", "Code"})
	t.AppendRow(table.Row{"Name:", repos.Code.Fullname})
	t.AppendRow(table.Row{"URL:", projectLib.CleanGitURL(repos.Code.Url)})
	t.AppendSeparator()
	t.AppendRow(table.Row{"", "Config"})
	t.AppendRow(table.Row{"Name:", repos.Configuration.Fullname})
	t.AppendRow(table.Row{"URL:", projectLib.CleanGitURL(repos.Configuration.Url)})

	// Render
	t.SetStyle(table.StyleLight)
	t.Render()
}
