package domainPrompts

import (
	"errors"
	"fmt"
	"strings"

	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/tools/tau/flags"
	domainI18n "github.com/taubyte/tau/tools/tau/i18n/domain"
	domainLib "github.com/taubyte/tau/tools/tau/lib/domain"
	"github.com/taubyte/tau/tools/tau/prompts"

	"github.com/urfave/cli/v2"
)

/*
GetOrSelect will try to get the domain from a name flag
if it is not set in the flag it will offer a selection menu
*/
func GetOrSelect(ctx *cli.Context) (*structureSpec.Domain, error) {
	name := ctx.String(flags.Name.Name)

	resources, err := domainLib.ListResources()
	if err != nil {
		return nil, err
	}

	// Try to select a domain
	if len(name) == 0 && len(resources) > 0 {
		options := make([]string, len(resources))
		for idx, p := range resources {
			options[idx] = p.Name
		}

		name, err = prompts.SelectInterface(options, SelectPrompt, options[0])
		if err != nil {
			return nil, domainI18n.SelectPromptFailed(err)
		}
	}

	if len(name) != 0 {
		nameLC := strings.ToLower(name)
		for _, domain := range resources {
			if nameLC == strings.ToLower(domain.Name) {
				return domain, nil
			}
		}

		return nil, fmt.Errorf(NotFound, name)
	}

	return nil, errors.New(NoneFound)
}
