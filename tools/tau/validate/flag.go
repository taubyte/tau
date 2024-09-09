package validate

import (
	"github.com/urfave/cli/v2"
)

func FlagEntryPointValidator(name string) Validator {
	return func(c *cli.Context) error {
		return VariableNameValidator(c.String(name))
	}
}

func FlagMethodTypeValidator(name string) Validator {
	return func(c *cli.Context) error {
		return MethodTypeValidator(c.String(name))
	}
}

func FlagApiMethodValidator(name string) Validator {
	return func(c *cli.Context) error {
		return ApiMethodValidator(c.String(name))
	}
}

func FlagCodeTypeValidator(name string) Validator {
	return func(c *cli.Context) error {
		return CodeTypeValidator(c.String(name))
	}
}

func FlagBucketValidator(name string) Validator {
	return func(c *cli.Context) error {
		return BucketTypeValidator(c.String(name))
	}
}
