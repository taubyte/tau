package websiteTable

import (
	"os"

	"github.com/jedib0t/go-pretty/v6/table"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	repositoryLib "github.com/taubyte/tau/tools/tau/lib/repository"
)

func List(libraries []*structureSpec.Website) {
	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)
	t.SetAllowedRowLength(79)

	t.AppendHeader(table.Row{"ID", "Name\nRepository"})
	for _, library := range libraries {
		id := library.Id
		if len(library.Id) >= 20 {
			id = id[:6] + "..." + id[len(id)-6:]
		}

		t.AppendRow(table.Row{id, library.Name})
		t.AppendRow(table.Row{"", repositoryLib.GetRepositoryUrl(library.Provider, library.RepoName)})
		t.AppendSeparator()
	}
	t.SetStyle(table.StyleLight)
	t.Render()
}
