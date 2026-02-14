package websitePrompts

import (
	"fmt"

	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/urfave/cli/v2"
)

func Edit(ctx *cli.Context, prev *structureSpec.Website) (any, error) {
	prev.Description = prompts.GetOrAskForADescription(ctx, prev.Description)
	prev.Tags = prompts.GetOrAskForTags(ctx, prev.Tags)

	var err error
	prev.Domains, err = prompts.GetOrSelectDomainsWithFQDN(ctx, prev.Domains...)
	if err != nil {
		return nil, err
	}

	fmt.Printf("[paths trace] website/edit.go before RequiredPaths prev.Paths=%q\n", prev.Paths)
	prev.Paths = prompts.RequiredPaths(ctx, prev.Paths...)
	fmt.Printf("[paths trace] website/edit.go after RequiredPaths prev.Paths=%q\n", prev.Paths)

	info, err := RepositoryInfo(ctx, prev, false)
	if err != nil {
		return nil, err
	}

	prev.Branch, err = prompts.GetOrRequireABranch(ctx, prev.Branch)
	if err != nil {
		return nil, err
	}

	return info, nil
}
