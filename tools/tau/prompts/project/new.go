package projectPrompts

import (
	projectLib "github.com/taubyte/tau/tools/tau/lib/project"
	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/urfave/cli/v2"
)

func New(ctx *cli.Context) (embedToken bool, project *projectLib.Project, err error) {
	project = &projectLib.Project{}

	projectNames, err := projectLib.List()
	if err != nil {
		return
	}

	project.Name, err = prompts.GetOrRequireAUniqueName(ctx, projectName, projectNames)
	if err != nil {
		return
	}
	project.Description = prompts.GetOrAskForADescription(ctx)
	project.Public, err = GetOrRequireVisibility(ctx)

	embedToken = prompts.GetOrAskForEmbedToken(ctx)

	return
}
