package prompts

import (
	"strings"

	"github.com/pterm/pterm"
	"github.com/taubyte/tau/pkg/git"
	"github.com/taubyte/tau/tools/tau/flags"
	"github.com/urfave/cli/v2"
)

func GetOrRequireABranch(c *cli.Context, prev ...string) string {
	return validateAndRequireString(c, validateRequiredStringHelper{
		field:  flags.Branch.Name,
		prompt: BranchPrompt,
		prev:   prev,

		// TODO Skipping validation, as a branch should be free.  Maybe use the same regex as github
	})
}

func SelectABranch(c *cli.Context, repo *git.Repository) (branch string, err error) {
	branchOptions, _, err := repo.ListBranches(true)
	if err != nil {
		return
	}

	if c.IsSet(flags.Branch.Name) {
		branchLC := strings.ToLower(c.String(flags.Branch.Name))

		for _, _branch := range branchOptions {
			if branchLC == strings.ToLower(_branch) {
				return _branch, nil
			}
		}

		pterm.Warning.Printfln("invalid branch: `%s`", branch)
	}

	return SelectInterface(branchOptions, BranchSelectPrompt, "")
}
