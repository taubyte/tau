package libraryPrompts

import (
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/urfave/cli/v2"
)

func Edit(ctx *cli.Context, prev *structureSpec.Library) (any, error) {
	prev.Description = prompts.GetOrAskForADescription(ctx, prev.Description)
	prev.Tags = prompts.GetOrAskForTags(ctx, prev.Tags)

	info, err := RepositoryInfo(ctx, prev, false)
	if err != nil {
		return nil, err
	}

	prev.Path, err = prompts.GetOrRequireAPath(ctx, "Path:", prev.Path)
	if err != nil {
		return nil, err
	}

	prev.Branch, err = prompts.GetOrRequireABranch(ctx, prev.Branch)
	if err != nil {
		return nil, err
	}

	return info, nil
}
