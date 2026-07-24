// Package validate holds the few checks the CLI still owns: shapes of things
// that live outside the configuration DSL (a project's name and description as
// the auth service accepts them, a cloud's FQDN). Everything a resource's
// fields must satisfy is declared in the DSL and checked by tcc.
package validate

import (
	"errors"
	"fmt"
	"regexp"

	"github.com/asaskevich/govalidator"
)

const (
	greaterThan250 = "must be less than 250 characters"
	between0And250 = "must be between 0 and 250 characters"

	mustStartWithALetter                       = "Must start with a letter"
	canOnlyContainLettersNumbersAndUnderscores = "Can only contain letters, numbers, underscores, and dashes"
)

var nameRegex = [][2]string{
	{mustStartWithALetter, `^[A-Za-z]`},
	{canOnlyContainLettersNumbersAndUnderscores, `^[a-zA-Z0-9_-]*$`},
}

func VariableNameValidator(val string) error {
	if val == "" {
		return nil
	}
	for _, exp := range nameRegex {
		match, err := regexp.MatchString(exp[1], val)
		if err != nil {
			return fmt.Errorf("invalid regex %q: %w", exp[1], err)
		}
		if !match {
			return errors.New(exp[0])
		}
	}
	if len(val) > 250 {
		return errors.New(between0And250)
	}
	return nil
}

func VariableDescriptionValidator(val string) error {
	if len(val) > 250 {
		return errors.New(greaterThan250)
	}
	return nil
}

func FQDNValidator(val string) error {
	if val != "" && !govalidator.IsDNSName(val) {
		return fmt.Errorf("invalid Fqdn: `%s`", val)
	}
	return nil
}
