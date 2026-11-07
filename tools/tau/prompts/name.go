package prompts

import (
	"fmt"
	"strings"

	"github.com/taubyte/tau/pkg/tcc/engine"
	"github.com/taubyte/tau/tools/tau/flags"
	"github.com/taubyte/tau/tools/tau/validate"
	"github.com/urfave/cli/v2"
	"golang.org/x/exp/slices"
)

func GetOrRequireAName(c *cli.Context, prompt string, prev ...string) (string, error) {
	return validateAndRequireString(c, validateRequiredStringHelper{
		field:     flags.Name.Name,
		prompt:    prompt,
		prev:      prev,
		validator: validate.VariableNameValidator,
	})
}

// ResourceNameValidator accepts only names the config engine accepts (no
// dashes), so a bad name fails here and not at validate or push.
func ResourceNameValidator(s string) error {
	if err := validate.VariableNameValidator(s); err != nil {
		return err
	}
	if err := engine.ValidVariableName(s); err != nil {
		return fmt.Errorf("`%s`: %w: use `%s`", s, err, strings.ReplaceAll(s, "-", "_"))
	}
	return nil
}

func GetOrRequireAUniqueName(c *cli.Context, prompt string, invalid []string, prev ...string) (string, error) {
	invalidLowerCase := make([]string, len(invalid))
	for idx, s := range invalid {
		invalidLowerCase[idx] = strings.ToLower(s)
	}

	return validateAndRequireString(c, validateRequiredStringHelper{
		field:  flags.Name.Name,
		prompt: prompt,
		prev:   prev,
		validator: func(s string) error {
			err := ResourceNameValidator(s)
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
