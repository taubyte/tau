package validate

import (
	"github.com/urfave/cli/v2"
)

type Validator func(c *cli.Context) error

type StringValidator func(name string) Validator

// EXAMPLE USAGE:
// ValidateFlag(NewProject,VariableNameValidator("name"),VariableDescriptionValidator("description"), VariableTagsValidator("tags"))
func ValidateFlag(action func(c *cli.Context) error, validators ...Validator) func(c *cli.Context) error {
	return func(c *cli.Context) error {
		for _, v := range validators {
			err := v(c)
			if err != nil {
				return err
			}
		}
		return action(c)
	}
}
