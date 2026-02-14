package prompts

import (
	"fmt"
	"strings"

	"github.com/AlecAivazis/survey/v2"
	"github.com/taubyte/tau/tools/tau/flags"
	"github.com/taubyte/tau/tools/tau/i18n/printer"
	"github.com/taubyte/tau/tools/tau/validate"
	"github.com/urfave/cli/v2"
)

func RequiredPaths(c *cli.Context, prev ...string) (ret []string) {
	fmt.Printf("[paths trace] RequiredPaths entered, prev=%q\n", prev)
	if c.IsSet(flags.Paths.Name) {
		_ret := c.StringSlice(flags.Paths.Name)
		fmt.Printf("[paths trace] RequiredPaths from flag StringSlice(%q)=%q\n", flags.Paths.Name, _ret)

		ret = make([]string, 0)
		for _, p := range _ret {
			err := validate.VariablePathValidator(p)
			if err != nil {
				printer.Out.Warning(err)
			} else {
				ret = append(ret, p)
			}
		}
		fmt.Printf("[paths trace] RequiredPaths after validation ret=%q\n", ret)
	}

	for len(ret) == 0 {
		var tempRet string

		AskOne(&survey.Input{
			Message: PathsPrompt,
			Default: strings.Join(prev, ","),
		}, &tempRet, survey.WithValidator(func(ans any) error {
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
		fmt.Printf("[paths trace] RequiredPaths after prompt cleanTags ret=%q\n", ret)
	}

	fmt.Printf("[paths trace] RequiredPaths returning ret=%q\n", ret)
	return ret
}
