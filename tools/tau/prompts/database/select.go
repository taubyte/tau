package databasePrompts

import (
	"errors"
	"fmt"
	"strings"

	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/tools/tau/flags"
	databaseI18n "github.com/taubyte/tau/tools/tau/i18n/database"
	databaseLib "github.com/taubyte/tau/tools/tau/lib/database"
	"github.com/taubyte/tau/tools/tau/prompts"

	"github.com/urfave/cli/v2"
)

/*
GetOrSelect will try to get the database from a name flag
if it is not set in the flag it will offer a selection menu
*/
func GetOrSelect(ctx *cli.Context) (*structureSpec.Database, error) {
	name := ctx.String(flags.Name.Name)

	resources, err := databaseLib.ListResources()
	if err != nil {
		return nil, err
	}

	// Try to select a database
	if len(name) == 0 && len(resources) > 0 {
		options := make([]string, len(resources))
		for idx, p := range resources {
			options[idx] = p.Name
		}

		name, err = prompts.SelectInterface(options, SelectPrompt, options[0])
		if err != nil {
			return nil, databaseI18n.SelectPromptFailed(err)
		}
	}

	if len(name) != 0 {
		for _, database := range resources {
			if strings.EqualFold(name, database.Name) {
				return database, nil
			}
		}

		return nil, fmt.Errorf(NotFound, name)
	}

	return nil, errors.New(NoneFound)
}
