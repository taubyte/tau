package servicePrompts

import (
	"errors"
	"fmt"
	"strings"

	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/tools/tau/flags"
	serviceI18n "github.com/taubyte/tau/tools/tau/i18n/service"
	serviceLib "github.com/taubyte/tau/tools/tau/lib/service"
	"github.com/taubyte/tau/tools/tau/prompts"

	"github.com/urfave/cli/v2"
)

/*
GetOrSelect will try to get the service from a name flag
if it is not set in the flag it will offer a selection menu
*/
func GetOrSelect(ctx *cli.Context) (*structureSpec.Service, error) {
	name := ctx.String(flags.Name.Name)

	resources, err := serviceLib.ListResources()
	if err != nil {
		return nil, err
	}

	// Try to select a service
	if len(name) == 0 && len(resources) > 0 {
		options := make([]string, len(resources))
		for idx, p := range resources {
			options[idx] = p.Name
		}

		name, err = prompts.SelectInterface(options, SelectPrompt, options[0])
		if err != nil {
			return nil, serviceI18n.SelectPromptFailed(err)
		}
	}

	if len(name) != 0 {
		nameLC := strings.ToLower(name)

		for _, service := range resources {
			if nameLC == strings.ToLower(service.Name) {
				return service, nil
			}
		}

		return nil, fmt.Errorf(NotFound, name)
	}

	return nil, errors.New(NoneFound)
}
