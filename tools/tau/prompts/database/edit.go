package databasePrompts

import (
	"github.com/taubyte/tau/pkg/schema/common"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/urfave/cli/v2"
)

func Edit(ctx *cli.Context, prev *structureSpec.Database) error {
	prev.Description = prompts.GetOrAskForADescription(ctx, prev.Description)
	prev.Tags = prompts.GetOrAskForTags(ctx, prev.Tags)

	prev.Regex = prompts.GetMatchRegex(ctx, prev.Regex)
	var err error
	prev.Match, err = GetOrRequireAMatch(ctx, prev.Match)
	if err != nil {
		return err
	}
	prev.Local = prompts.GetOrAskForLocal(ctx, prev.Local)

	if GetEncryption(ctx, len(prev.Key) > 0) {
		prev.Key, err = GetOrRequireAnEncryptionKey(ctx, prev.Key)
		if err != nil {
			return err
		}
	} else {
		prev.Key = ""
	}

	prev.Min, prev.Max, _, _, err = GetOrAskForMinMax(ctx, prev.Min, prev.Max, false)
	if err != nil {
		return err
	}

	sizeStr, err := prompts.GetSizeAndType(ctx, common.UnitsToString(prev.Size), false)
	if err != nil {
		return err
	}
	prev.Size, err = common.StringToUnits(sizeStr)
	if err != nil {
		return err
	}

	return nil
}
