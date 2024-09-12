package prompts

import (
	"fmt"
	"strings"

	"github.com/taubyte/tau/tools/tau/flags"
	"github.com/taubyte/tau/tools/tau/validate"
	"github.com/urfave/cli/v2"
	"golang.org/x/exp/slices"
)

func GetOrRequireAName(c *cli.Context, prompt string, prev ...string) string {
	return validateAndRequireString(c, validateRequiredStringHelper{
		field:     flags.Name.Name,
		prompt:    prompt,
		prev:      prev,
		validator: validate.VariableNameValidator,
	})
}

func GetOrRequireAUniqueName(c *cli.Context, prompt string, invalid []string, prev ...string) string {
	invalidLowerCase := make([]string, len(invalid))
	for idx, s := range invalid {
		invalidLowerCase[idx] = strings.ToLower(s)
	}

	return validateAndRequireString(c, validateRequiredStringHelper{
		field:  flags.Name.Name,
		prompt: prompt,
		prev:   prev,
		validator: func(s string) error {
			err := validate.VariableNameValidator(s)
			if err != nil {
				return err
			}

			if slices.Contains(invalidLowerCase, strings.ToLower(s)) {
				return fmt.Errorf("`%s` is already taken: `%v`", s, invalid)
			}

			return nil
		},
	})
}
