package websitePrompts

import (
	"fmt"

	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	websiteLib "github.com/taubyte/tau/tools/tau/lib/website"
	"github.com/taubyte/tau/tools/tau/prompts"
	loginPrompts "github.com/taubyte/tau/tools/tau/prompts/login"
	"github.com/urfave/cli/v2"
)

func New(ctx *cli.Context) (any, *structureSpec.Website, error) {
	website := &structureSpec.Website{}

	taken, err := websiteLib.List()
	if err != nil {
		return nil, nil, err
	}

	website.Name, err = prompts.GetOrRequireAUniqueName(ctx, NamePrompt, taken)
	if err != nil {
		return nil, nil, err
	}
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

	fmt.Printf("[paths trace] website/new.go before RequiredPaths\n")
	website.Paths = prompts.RequiredPaths(ctx)
	fmt.Printf("[paths trace] website/new.go after RequiredPaths website.Paths=%q\n", website.Paths)

	info, err := RepositoryInfo(ctx, website, true)
	if err != nil {
		return nil, nil, err
	}

	website.Branch, err = prompts.GetOrRequireABranch(ctx)
	if err != nil {
		return nil, nil, err
	}

	return info, website, nil
}
