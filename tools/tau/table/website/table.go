package websiteTable

import (
	"fmt"
	"strings"

	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	websiteLib "github.com/taubyte/tau/tools/tau/lib/website"
)

func getTableData(website *structureSpec.Website, showId bool) (toRender [][]string) {
	fmt.Printf("[paths trace] table/website/table.go getTableData website.Paths=%q\n", website.Paths)
	if showId {
		toRender = [][]string{
			{"ID", website.Id},
		}
	}

	toRender = append(toRender, [][]string{
		{"Name", website.Name},
		{"Description", website.Description},
		{"Tags", strings.Join(website.Tags, ", ")},
		{"Paths", strings.Join(website.Paths, ", ")},
		{"Domains", strings.Join(website.Domains, ", ")},
	}...)

	toRender = append(toRender, [][]string{
		{"Repository", websiteLib.GetRepositoryUrl(website)},
		{"\tName", website.RepoName},
		{"\tID", website.RepoID},
		{"\tProvider", website.Provider},
		{"\tBranch", website.Branch},
	}...)

	return toRender
}
