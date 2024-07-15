package validate

import (
	"strings"

	slices "github.com/taubyte/utils/slices/string"
	"github.com/urfave/cli/v2"
)

func cleanPaths(pathString string) []string {
	// TODO: Replace with regex
	ret_str := strings.Replace(pathString, " ", "", -1)
	ret_str = strings.Replace(ret_str, "\t", "", -1)
	ret_str = strings.Replace(ret_str, "\n", "", -1)
	ret_map := strings.Split(ret_str, ",")

	return slices.Unique(ret_map)
}

func FlagNameValidator(name string) Validator {
	return func(c *cli.Context) error {
		return VariableNameValidator(c.String(name))
	}
}

func FlagDescriptionValidator(name string) Validator {
	return func(c *cli.Context) error {
		return VariableDescriptionValidator(c.String(name))
	}
}

func FlagTagsValidator(name string) Validator {
	return func(c *cli.Context) error {
		tags := c.String(name)
		return VariableTagsValidator(strings.Split(tags, ","))
	}
}

func FlagPathValidator(name string) Validator {
	return func(c *cli.Context) error {
		return VariablePathValidator(c.String(name))
	}
}

func FlagPathsValidator(name string) Validator {
	return func(c *cli.Context) error {
		paths := cleanPaths(c.String(name))
		var err error
		for _, path := range paths {
			err = VariablePathValidator(path)
			if err != nil {
				return err
			}
		}
		return nil
	}
}

func FlagEntryPointValidator(name string) Validator {
	return func(c *cli.Context) error {
		return VariableNameValidator(c.String(name))
	}
}

func FlagBoolValidator(name string) Validator {
	return func(c *cli.Context) error {
		return VariableBool(c.String(name))
	}
}

func FlagBasicValidator(name string) Validator {
	return func(c *cli.Context) error {
		return VariableRequiredValidator(c.String(name))
	}
}

func FlagProviderValidator(name string) Validator {
	return func(c *cli.Context) error {
		return VariableProviderValidator(c.String(name))
	}
}

func FlagIntValidator(name string) Validator {
	return func(c *cli.Context) error {
		return VariableIntValidator(c.String(name))
	}
}

func FlagUnitSizeValidator(name string) Validator {
	return func(c *cli.Context) error {
		return SizeUnitValidator(c.String(name))
	}
}

func FlagSizeValidator(name string) Validator {
	return func(c *cli.Context) error {
		return VariableSizeValidator(c.String(name))
	}
}

func FlagFQDNValidator(name string) Validator {
	return func(c *cli.Context) error {
		return FQDNValidator(c.String(name))
	}
}

func FlagBasicNoCharLimit(name string) Validator {
	return func(c *cli.Context) error {
		return RequiredNoCharLimit(c.String(name))
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

func FlagTimeValidator(name string) Validator {
	return func(c *cli.Context) error {
		return VariableTime(c.String(name))
	}
}

func FlagBucketValidator(name string) Validator {
	return func(c *cli.Context) error {
		return BucketTypeValidator(c.String(name))
	}
}
