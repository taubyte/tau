package websitePrompts

import (
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	websiteLib "github.com/taubyte/tau/tools/tau/lib/website"
	"github.com/taubyte/tau/tools/tau/prompts"
	loginPrompts "github.com/taubyte/tau/tools/tau/prompts/login"
	"github.com/urfave/cli/v2"
)

func New(ctx *cli.Context) (interface{}, *structureSpec.Website, error) {
	website := &structureSpec.Website{}

	taken, err := websiteLib.List()
	if err != nil {
		return nil, nil, err
	}

	website.Name = prompts.GetOrRequireAUniqueName(ctx, NamePrompt, taken)
	website.Description = prompts.GetOrAskForADescription(ctx)
	website.Tags = prompts.GetOrAskForTags(ctx)

	website.Provider, err = loginPrompts.SelectAProvider(ctx)
	if err != nil {
		return nil, nil, err
	}

	website.Domains, err = prompts.GetOrSelectDomainsWithFQDN(ctx)
	if err != nil {
		return nil, nil, err
	}

	website.Paths = prompts.RequiredPaths(ctx)

	info, err := RepositoryInfo(ctx, website, true)
	if err != nil {
		return nil, nil, err
	}

	website.Branch = prompts.GetOrRequireABranch(ctx)

	return info, website, nil
}
