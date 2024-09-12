package prompts

import (
	"strings"

	"github.com/AlecAivazis/survey/v2"
	"github.com/pterm/pterm"
	"github.com/taubyte/tau/tools/tau/flags"
	slices "github.com/taubyte/utils/slices/string"
	"github.com/urfave/cli/v2"
)

func cleanTags(tagString string) []string {
	if len(tagString) == 0 {
		return nil
	}

	// TODO: Replace with regex
	ret_str := strings.Replace(tagString, " ", "", -1)
	ret_str = strings.Replace(ret_str, "\t", "", -1)
	ret_str = strings.Replace(ret_str, "\n", "", -1)
	ret_map := strings.Split(ret_str, ",")

	return slices.Unique(ret_map)
}

func checkEmpty(tags []string) (empty bool) {
	if len(tags) == 0 {
		return true
	}
	for _, tag := range tags {
		if tag == "" {
			empty = true
		}
	}
	return
}

func RequiredTags(c *cli.Context, prev ...[]string) (ret []string) {
	ret = c.StringSlice(flags.Tags.Name)

	var firstRun bool
	for checkEmpty(ret) {
		if !firstRun {
			firstRun = true
		} else {
			pterm.Warning.Println(Required)
		}

		ret = askForTags(c, prev...)
	}

	return
}

func GetOrAskForTags(c *cli.Context, prev ...[]string) (ret []string) {
	ret = c.StringSlice(flags.Tags.Name)
	if len(ret) > 0 {
		return
	}

	return askForTags(c, prev...)
}

func askForTags(c *cli.Context, prev ...[]string) []string {
	panicIfPromptNotEnabled("tags")

	var val string

	inp := &survey.Input{
		Message: TagsPrompt,
	}
	if len(prev) == 1 {
		inp.Default = strings.Join(prev[0][:], ", ")
	}

	AskOne(inp, &val)

	return cleanTags(val)
}
