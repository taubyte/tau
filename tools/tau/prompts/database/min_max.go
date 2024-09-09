package databasePrompts

import (
	"fmt"
	"strconv"

	"github.com/pterm/pterm"
	databaseFlags "github.com/taubyte/tau/tools/tau/flags/database"
	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/taubyte/tau/tools/tau/validate"
	"github.com/urfave/cli/v2"
)

func getOrRequireMin(c *cli.Context, prev ...string) string {
	return prompts.GetOrRequireAString(c, databaseFlags.Min.Name, MinPrompt, validate.VariableMinValidator, prev...)
}

func getOrRequireMax(c *cli.Context, prev ...string) string {
	return prompts.GetOrRequireAString(c, databaseFlags.Max.Name, MaxPrompt, validate.VariableMaxValidator, prev...)
}

func GetOrAskForMinMax(c *cli.Context, prevMin, prevMax int, new bool) (intMin int, intMax int, min string, max string) {
	var err error
	for {
		if new {
			min = getOrRequireMin(c)
			max = getOrRequireMax(c)
		} else {
			min = getOrRequireMin(c, strconv.Itoa(prevMin))
			max = getOrRequireMax(c, strconv.Itoa(prevMax))
		}

		intMin, intMax, err = convertAndValidateMinMax(min, max)
		if err != nil {
			pterm.Warning.Println(err.Error())
			prompts.PanicIfPromptNotEnabled("min-max prompt")
		} else {
			break
		}
	}

	return
}

func convertAndValidateMinMax(min string, max string) (_min int, _max int, err error) {
	_min, err = strconv.Atoi(min)
	if err != nil {
		return 0, 0, fmt.Errorf(ParsingMinFailed, min, err)
	}

	_max, err = strconv.Atoi(max)
	if err != nil {
		return 0, 0, fmt.Errorf(ParsingMaxFailed, max, err)
	}

	if _min >= _max {
		return 0, 0, fmt.Errorf(MinCannotBeGreaterThanMax, min, max)
	}

	return
}
