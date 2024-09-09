package projectPrompts

import (
	"strings"

	client "github.com/taubyte/tau/clients/http/auth"
	"github.com/taubyte/tau/tools/tau/env"
	"github.com/taubyte/tau/tools/tau/flags"
	projectI18n "github.com/taubyte/tau/tools/tau/i18n/project"
	projectLib "github.com/taubyte/tau/tools/tau/lib/project"
	"github.com/taubyte/tau/tools/tau/prompts"

	"github.com/urfave/cli/v2"
)

/*
GetOrSelect will try to get the project from a name flag
if it is not set in the flag it will offer a selection menu
*/
func GetOrSelect(ctx *cli.Context, checkEnv bool) (*client.Project, error) {
	name := ctx.String(flags.Name.Name)

	// Try to get selected project
	if len(name) == 0 && checkEnv {
		name, _ = env.GetSelectedProject()
	}

	projects, err := projectLib.ListResources()
	if err != nil {
		return nil, projectI18n.GettingProjectsFailed(err)
	}

	// Try to select a project
	if len(name) == 0 && len(projects) > 0 {
		projectOptions := make([]string, len(projects))
		for idx, p := range projects {
			projectOptions[idx] = p.Name
		}

		name, err = prompts.SelectInterface(projectOptions, selectAProject, projectOptions[0])
		if err != nil {
			return nil, projectI18n.SelectingAProjectPromptFailed(err)
		}
	}

	if len(name) != 0 {
		project, err := matchLowercase(name, projects)
		if err != nil {
			return nil, err
		}

		return project, nil
	}

	return nil, projectI18n.ErrorNoProjectsFound
}

func GetSelectOrDeselect(ctx *cli.Context) (project *client.Project, deselect bool, err error) {
	currentlySelected, _ := env.GetSelectedProject()
	if len(currentlySelected) == 0 {
		project, err = GetOrSelect(ctx, false)
		return
	}

	name := ctx.String(flags.Name.Name)
	projects, err := projectLib.ListResources()
	if err != nil {
		return nil, false, projectI18n.GettingProjectsFailed(err)
	}

	options := make([]string, len(projects)+1 /*accounting for (none)*/)
	for idx, _project := range projects {
		options[idx] = _project.Name
	}

	options[len(options)-1] = prompts.SelectionNone

	// Try to select a project
	if len(name) == 0 && len(options) > 1 {
		name, err = prompts.SelectInterface(options, selectAProject, currentlySelected)
		if err != nil {
			err = projectI18n.SelectingAProjectPromptFailed(err)
			return
		}
	}

	if len(name) > 0 {
		var deselect bool
		if name == prompts.SelectionNone {
			deselect = true
			name = currentlySelected
		}

		project, err = matchLowercase(name, projects)
		if err != nil {
			return nil, false, err
		}

		return project, deselect, nil
	}

	return nil, false, projectI18n.ErrorNoProjectsFound
}

func matchLowercase(name string, projects []*client.Project) (*client.Project, error) {
	nameLC := strings.ToLower(name)

	for _, project := range projects {
		if nameLC == strings.ToLower(project.Name) {
			return project, nil
		}
	}

	return nil, projectI18n.ProjectNotFound(name)
}
