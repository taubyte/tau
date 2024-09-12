package websitePrompts

import (
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/urfave/cli/v2"
)

func Edit(ctx *cli.Context, prev *structureSpec.Website) (interface{}, error) {
	prev.Description = prompts.GetOrAskForADescription(ctx, prev.Description)
	prev.Tags = prompts.GetOrAskForTags(ctx, prev.Tags)

	var err error
	prev.Domains, err = prompts.GetOrSelectDomainsWithFQDN(ctx, prev.Domains...)
	if err != nil {
		return nil, err
	}

	prev.Paths = prompts.RequiredPaths(ctx, prev.Paths...)

	info, err := RepositoryInfo(ctx, prev, false)
	if err != nil {
		return nil, err
	}

	prev.Branch = prompts.GetOrRequireABranch(ctx, prev.Branch)

	return info, nil
}
