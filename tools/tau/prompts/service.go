package prompts

import (
	"fmt"
	"strings"

	"github.com/pterm/pterm"
	"github.com/taubyte/tau/pkg/schema/project"
	serviceSchema "github.com/taubyte/tau/pkg/schema/services"
	"github.com/taubyte/tau/tools/tau/env"
	projectLib "github.com/taubyte/tau/tools/tau/lib/project"
	"github.com/urfave/cli/v2"
)

func generateServiceOption(service serviceSchema.Service) string {
	getter := service.Get()

	app := getter.Application()
	if len(app) > 0 {
		return fmt.Sprintf("%s/%s ( %s )", app, getter.Name(), getter.Protocol())
	}

	return fmt.Sprintf("%s ( %s )", getter.Name(), getter.Protocol())
}

/*
	buildServiceOptions takes services from the services flag and previously selected

Parameters:

	flagServiceLowerCase: the service name or Protocol parsed from the service flag
	prev: previously selected service name

Returns:

	flagService: selected service based on flag provided
	previous: the previously selected service option
	options: possible selections
	optionMap: maps the selected options back to the service name
	err: an error
*/
func buildServiceOptions(flagServiceLowerCase string, prev ...string) (flagService, previous string, options []string, optionMap map[string]string, err error) {
	var project project.Project
	project, err = projectLib.SelectedProjectInterface()
	if err != nil {
		return
	}

	// Get possible services from config
	selectedApp, _ := env.GetSelectedApplication()
	local, global := project.Get().Services(selectedApp)

	// Build options and find potential selections
	options = make([]string, 0)

	// Maps the option (name/protocol) back to the name of the service
	optionMap = make(map[string]string)

	generator := func(name string, service serviceSchema.Service) {
		option := generateServiceOption(service)
		options = append(options, option)
		optionMap[option] = name

		for _, _prev := range prev {
			if len(previous) == 0 && name == _prev {
				previous = option

			}
		}

		// Get selection from flag
		if len(flagServiceLowerCase) > 0 {
			nameLC := strings.ToLower(name)
			protocolLC := strings.ToLower(service.Get().Protocol())
			if flagServiceLowerCase == nameLC || flagServiceLowerCase == protocolLC {
				if len(flagService) == 0 {
					flagService = name
				}
			}
		}
	}

	var service serviceSchema.Service
	for _, name := range local {
		service, err = project.Service(name, selectedApp)
		if err != nil {
			return
		}

		generator(name, service)
	}

	for _, name := range global {
		service, err = project.Service(name, "")
		if err != nil {
			return
		}

		generator(name, service)
	}

	return
}

func SelectAServiceWithProtocol(ctx *cli.Context, field string, prompt string, prev ...string) (string, error) {
	// Names or Protocols
	flagServiceLowerCase := strings.ToLower(ctx.String(field))

	flagService, previous, options, optionMap, err := buildServiceOptions(flagServiceLowerCase, prev...)
	if err != nil {
		return "", err
	}

	// Simply return if we get service from flag
	if len(flagService) > 0 {
		return flagService, nil
	}

	if len(options) == 0 {
		return "", ErrorNoServicesDefined
	}

	// Display an error if no matches found for flag
	if len(flagServiceLowerCase) > 0 {
		pterm.Warning.Printfln(NoServiceFromFlag, field, ctx.String(field))
	}

	selected, err := SelectInterface(options, prompt, previous)
	if err != nil {
		return "", err
	}

	service, ok := optionMap[selected]
	if !ok {
		// Should never get here, as options are generated
		return "", fmt.Errorf("unable to find service for selection: %s", selected)
	}

	return service, nil
}
