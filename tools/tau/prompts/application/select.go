package applicationPrompts

import (
	"errors"
	"fmt"
	"strings"

	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/tools/tau/env"
	"github.com/taubyte/tau/tools/tau/flags"
	applicationI18n "github.com/taubyte/tau/tools/tau/i18n/application"
	applicationLib "github.com/taubyte/tau/tools/tau/lib/application"
	"github.com/taubyte/tau/tools/tau/prompts"

	"github.com/urfave/cli/v2"
)

/*
GetOrSelect will try to get the application from a name flag
if it is not set in the flag it will offer a selection menu
*/
func GetOrSelect(ctx *cli.Context, checkEnv bool) (*structureSpec.App, error) {
	name := ctx.String(flags.Name.Name)

	// Try to get selected application
	if len(name) == 0 && checkEnv {
		name, _ = env.GetSelectedApplication()
	}

	resources, err := applicationLib.ListResources()
	if err != nil {
		return nil, err
	}

	// Try to select a application
	if len(name) == 0 && len(resources) > 0 {
		// TODO currentlySelected should be an option surrounded by ()
		// acting as a deselect

		options := make([]string, len(resources))
		for idx, p := range resources {
			options[idx] = p.Name
		}

		name, err = prompts.SelectInterface(options, SelectPrompt, options[0])
		if err != nil {
			return nil, applicationI18n.SelectPromptFailed(err)
		}
	}

	if len(name) != 0 {
		app, err := matchLowercase(name, resources)
		if err != nil {
			return nil, err
		}

		return app, nil
	}

	return nil, errors.New(NoneFound)
}

func GetSelectOrDeselect(ctx *cli.Context) (app *structureSpec.App, deselect bool, err error) {
	currentlySelected, _ := env.GetSelectedApplication()
	if len(currentlySelected) == 0 {
		app, err = GetOrSelect(ctx, false)
		if err != nil {
			return
		}

		return app, false, nil
	}

	name := ctx.String(flags.Name.Name)
	resources, err := applicationLib.ListResources()
	if err != nil {
		return nil, false, err
	}

	options := make([]string, len(resources)+1 /*accounting for (none)*/)
	for idx, _app := range resources {
		options[idx] = _app.Name
	}

	options[len(options)-1] = prompts.SelectionNone

	// Try to select a application
	if len(name) == 0 && len(options) > 0 {
		name, err = prompts.SelectInterface(options, SelectPrompt, currentlySelected)
		if err != nil {
			return nil, false, applicationI18n.SelectPromptFailed(err)
		}
	}

	if len(name) > 0 {
		var deselect bool
		if name == prompts.SelectionNone {
			deselect = true
			name = currentlySelected
		}

		app, err := matchLowercase(name, resources)
		if err != nil {
			return nil, false, err
		}

		return app, deselect, nil
	}

	return nil, false, errors.New(NoneFound)
}

func matchLowercase(name string, apps []*structureSpec.App) (*structureSpec.App, error) {
	nameLC := strings.ToLower(name)

	for _, app := range apps {
		if nameLC == strings.ToLower(app.Name) {
			return app, nil
		}
	}

	return nil, fmt.Errorf(NotFound, name)
}
