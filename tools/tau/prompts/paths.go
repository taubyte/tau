package prompts

import (
	"fmt"
	"strings"

	"github.com/AlecAivazis/survey/v2"
	"github.com/pterm/pterm"
	"github.com/taubyte/tau/tools/tau/flags"
	"github.com/taubyte/tau/tools/tau/validate"
	"github.com/urfave/cli/v2"
)

func RequiredPaths(c *cli.Context, prev ...string) (ret []string) {
	if c.IsSet(flags.Paths.Name) {
		_ret := c.StringSlice(flags.Paths.Name)

		ret = make([]string, 0)
		for _, p := range _ret {
			err := validate.VariablePathValidator(p)
			if err != nil {
				pterm.Warning.Println(err)
			} else {
				ret = append(ret, p)
			}
		}
	}

	for len(ret) == 0 {
		var tempRet string

		AskOne(&survey.Input{
			Message: PathsPrompt,
			Default: strings.Join(prev, ","),
		}, &tempRet, survey.WithValidator(func(ans interface{}) error {
			stringAns := ans.(string)
			if len(stringAns) == 0 {
				return fmt.Errorf(StringIsRequired, flags.Paths.Name)
			}
			ret = cleanTags(stringAns)
			for _, p := range ret {
				err := validate.VariablePathValidator(p)
				if err != nil {
					return err
				}
			}

			return nil
		}))

		ret = cleanTags(tempRet)
	}

	return ret
}
