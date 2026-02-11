package databasePrompts

import (
	"github.com/taubyte/tau/pkg/schema/common"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	databaseLib "github.com/taubyte/tau/tools/tau/lib/database"
	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/urfave/cli/v2"
)

func New(ctx *cli.Context) (*structureSpec.Database, error) {
	database := &structureSpec.Database{}

	taken, err := databaseLib.List()
	if err != nil {
		return nil, err
	}

	database.Name, err = prompts.GetOrRequireAUniqueName(ctx, NamePrompt, taken)
	if err != nil {
		return nil, err
	}
	database.Description = prompts.GetOrAskForADescription(ctx)
	database.Tags = prompts.GetOrAskForTags(ctx)

	database.Regex = prompts.GetMatchRegex(ctx)
	database.Match, err = GetOrRequireAMatch(ctx)
	if err != nil {
		return nil, err
	}
	database.Local = prompts.GetOrAskForLocal(ctx)

	if GetEncryption(ctx) {
		database.Key, err = GetOrRequireAnEncryptionKey(ctx)
		if err != nil {
			return nil, err
		}
	}

	database.Min, database.Max, _, _, err = GetOrAskForMinMax(ctx, 0, 0, true)
	if err != nil {
		return nil, err
	}

	sizeStr, err := prompts.GetSizeAndType(ctx, "", true)
	if err != nil {
		return nil, err
	}
	database.Size, err = common.StringToUnits(sizeStr)
	if err != nil {
		return nil, err
	}

	return database, nil
}
