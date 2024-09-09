package flags

import (
	"fmt"

	"github.com/taubyte/tau/tools/tau/singletons/templates"
	"github.com/urfave/cli/v2"
)

func GeneratedRepoUsage(format string) string {
	return "Generate a new repository using provided name or " + fmt.Sprintf(format, "<name>")
}

var GenerateRepo = &BoolWithInverseFlag{
	BoolFlag: &cli.BoolFlag{
		Name:    "generate-repository",
		Aliases: []string{"g"},
		Usage:   "Create a new repository on your selected git provider",
	},
}

var RepositoryName = &cli.StringFlag{
	Name:    "repository-name",
	Aliases: []string{"repo-n"},
	Usage:   "Name of a repository with current user or user/name full name of a repository",
}

var RepositoryId = &cli.StringFlag{
	Name:    "repository-id",
	Aliases: []string{"repo-id"},
	Usage:   "Repository ID to use",
}

var Template = &cli.StringFlag{
	Name:  "template",
	Usage: "See: " + templates.TemplateRepoURL,
}
