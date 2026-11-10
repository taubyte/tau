package projectPrompts

import (
	"strings"

	"github.com/taubyte/tau/tools/tau/flags"
	projectFlags "github.com/taubyte/tau/tools/tau/flags/project"
	projectLib "github.com/taubyte/tau/tools/tau/lib/project"
	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/urfave/cli/v2"
)

func New(ctx *cli.Context) (embedToken bool, project *projectLib.Project, err error) {
	project = &projectLib.Project{}

	// A bad --name is refused before listing projects calls auth (and GitHub).
	if name := ctx.String(flags.Name.Name); name != "" {
		if err = prompts.ResourceNameValidator(name); err != nil {
			return
		}
	}

	projects, err := projectLib.ListResources()
	if err != nil {
		return
	}

	// The name of a project that never finished is free to finish it with.
	wanted := ctx.String(flags.Name.Name)
	projectNames := make([]string, 0, len(projects))
	for _, p := range projects {
		if project.Resume == nil && strings.EqualFold(p.Name, wanted) && p.Unfinished() {
			project.Resume = p
			continue
		}
		projectNames = append(projectNames, p.Name)
	}

	project.Name, err = prompts.GetOrRequireAUniqueName(ctx, projectName, projectNames)
	if err != nil {
		return
	}
	project.Description = prompts.GetOrAskForADescription(ctx)
	project.Public, err = GetOrRequireVisibility(ctx)
	if err != nil {
		return
	}

	project.Account, project.Plan, err = projectLib.BindingFlags(
		ctx.String(projectFlags.Account.Name),
		ctx.String(projectFlags.Plan.Name),
	)
	if err != nil {
		return
	}

	embedToken = prompts.GetOrAskForEmbedToken(ctx)

	return
}
