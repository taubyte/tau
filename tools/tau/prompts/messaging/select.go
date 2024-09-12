package messagingPrompts

import (
	"errors"
	"fmt"
	"strings"

	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/tools/tau/flags"
	messagingI18n "github.com/taubyte/tau/tools/tau/i18n/messaging"
	messagingLib "github.com/taubyte/tau/tools/tau/lib/messaging"
	"github.com/taubyte/tau/tools/tau/prompts"

	"github.com/urfave/cli/v2"
)

/*
GetOrSelect will try to get the messaging from a name flag
if it is not set in the flag it will offer a selection menu
*/
func GetOrSelect(ctx *cli.Context) (*structureSpec.Messaging, error) {
	name := ctx.String(flags.Name.Name)

	resources, err := messagingLib.ListResources()
	if err != nil {
		return nil, err
	}

	// Try to select a messaging
	if len(name) == 0 && len(resources) > 0 {
		options := make([]string, len(resources))
		for idx, p := range resources {
			options[idx] = p.Name
		}

		name, err = prompts.SelectInterface(options, SelectPrompt, options[0])
		if err != nil {
			return nil, messagingI18n.SelectPromptFailed(err)
		}
	}

	if len(name) != 0 {
		nameLC := strings.ToLower(name)
		for _, messaging := range resources {
			if nameLC == strings.ToLower(messaging.Name) {
				return messaging, nil
			}
		}

		return nil, fmt.Errorf(NotFound, name)
	}

	return nil, errors.New(NoneFound)
}
