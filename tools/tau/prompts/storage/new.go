package storagePrompts

import (
	"fmt"

	"github.com/taubyte/tau/pkg/schema/common"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	storageLib "github.com/taubyte/tau/tools/tau/lib/storage"
	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/urfave/cli/v2"
)

func New(ctx *cli.Context) (*structureSpec.Storage, error) {
	storage := &structureSpec.Storage{}

	taken, err := storageLib.List()
	if err != nil {
		return nil, err
	}

	storage.Name = prompts.GetOrRequireAUniqueName(ctx, NamePrompt, taken)
	storage.Description = prompts.GetOrAskForADescription(ctx)
	storage.Tags = prompts.GetOrAskForTags(ctx)

	storage.Regex = prompts.GetMatchRegex(ctx)
	storage.Match = GetOrRequireAMatch(ctx)
	storage.Public = GetPublic(ctx)

	size, err := common.StringToUnits(prompts.GetSizeAndType(ctx, "", true))
	if err != nil {
		// TODO verbose
		return nil, err
	}
	storage.Size = uint64(size)

	// Streaming or Object
	storage.Type = SelectABucket(ctx)
	if err != nil {
		return nil, err
	}
	switch storage.Type {
	case storageLib.BucketStreaming:
		err = newStreaming(ctx, storage)
	case storageLib.BucketObject:
		err = newObject(ctx, storage)
	default:
		// Should not get here
		return nil, fmt.Errorf("invalid bucket: %s", storage.Type)
	}

	if err != nil {
		return nil, err
	}

	return storage, err
}

func newStreaming(ctx *cli.Context, storage *structureSpec.Storage) error {
	var err error
	storage.Ttl, err = prompts.GetOrRequireATimeout(ctx)
	if err != nil {
		// TODO verbose error i18n
		return err
	}

	return nil
}

func newObject(ctx *cli.Context, storage *structureSpec.Storage) error {
	storage.Versioning = GetVersioning(ctx)
	return nil
}
