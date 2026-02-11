package storagePrompts

import (
	"fmt"

	"github.com/taubyte/tau/pkg/schema/common"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	storageLib "github.com/taubyte/tau/tools/tau/lib/storage"
	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/urfave/cli/v2"
)

func Edit(ctx *cli.Context, prev *structureSpec.Storage) error {
	prev.Description = prompts.GetOrAskForADescription(ctx, prev.Description)
	prev.Tags = prompts.GetOrAskForTags(ctx, prev.Tags)

	prev.Regex = prompts.GetMatchRegex(ctx, prev.Regex)
	var err error
	prev.Match, err = GetOrRequireAMatch(ctx, prev.Match)
	if err != nil {
		return err
	}

	prev.Public = GetPublic(ctx, prev.Public)

	sizeStr, err := prompts.GetSizeAndType(ctx, common.UnitsToString(prev.Size), false)
	if err != nil {
		return err
	}
	size, err := common.StringToUnits(sizeStr)
	if err != nil {
		return err
	}
	prev.Size = uint64(size)

	// Streaming or Object
	prev.Type, err = SelectABucket(ctx, prev.Type)
	if err != nil {
		return err
	}
	switch prev.Type {
	case storageLib.BucketStreaming:
		return editStreaming(ctx, prev)
	case storageLib.BucketObject:
		return editObject(ctx, prev)
	default:
		// Should not get here
		return fmt.Errorf("invalid bucket: %s", prev.Type)
	}
}

func editStreaming(ctx *cli.Context, prev *structureSpec.Storage) error {
	var err error
	prev.Ttl, err = prompts.GetOrRequireATimeout(ctx, prev.Ttl)
	if err != nil {
		return err
	}

	return nil
}

func editObject(ctx *cli.Context, prev *structureSpec.Storage) error {
	prev.Versioning = GetVersioning(ctx, prev.Versioning)
	return nil
}
