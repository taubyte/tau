package validate

import (
	"errors"

	"github.com/urfave/cli/v2"
)

func VariableTypeValidator(val string) error {
	if val != "" {
		if len(val) > 250 {
			return errors.New(GreaterThan250)
		}
	}
	return nil
}

func FlagTypeValidator(name string) Validator {
	return func(c *cli.Context) error {
		return VariableTypeValidator(c.String(name))
	}
}
