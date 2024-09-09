package validate

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/asaskevich/govalidator"
	cliCommon "github.com/taubyte/tau/pkg/cli/common"
	"github.com/taubyte/tau/tools/tau/common"

	"github.com/taubyte/tau/tools/tau/constants"
)

func SliceContains(slice []string, str string) bool {
	for _, v := range slice {
		if v == str {
			return true
		}
	}

	return false
}

func VariableNameValidator(val string) error {
	if val != "" {
		err := matchAllString(val, NameRegex)
		if err != nil {
			return err
		}
		if len(val) == 0 || len(val) > 250 {
			return errors.New(Between0And250)
		}
	}

	return nil
}

func VariableDescriptionValidator(val string) error {
	if val != "" {
		if len(val) > 250 {
			return errors.New(GreaterThan250)
		}
	}

	return nil
}

func VariableTagsValidator(val []string) error {
	// TODO validate

	// Example:
	// for _, v := range val{
	// 	  err := matchAllString(val, TagRegex)
	// }
	return nil
}

func VariablePathValidator(path string) error {
	if path != "" {
		if !strings.HasPrefix(path, "/") {
			return fmt.Errorf(PathMustStartWithSlash, path)
		}
	}

	// TODO REGEX
	return nil
}

func VariableTime(val string) error {
	if val != "" {
		_, err := time.ParseDuration(val)
		if err != nil {
			return fmt.Errorf(InvalidTimeInput, val, err)
		}
	}

	return nil
}

func VariableBool(val string) error {
	if val != "" {
		if _, err := strconv.ParseBool(val); err != nil {
			return fmt.Errorf(InvalidBoolInput, val, err)
		}
	}

	return nil
}

func VariableRequiredValidator(val string) error {
	if val != "" {
		if len(val) == 0 || len(val) > 250 {
			return errors.New(Between0And250)
		}
	}

	return nil
}

func VariableProviderValidator(val string) error {
	if val != "" {
		// TODO: add || gitlab || bitbucket when implemented
		if strings.ToLower(val) != "github" {
			return fmt.Errorf(ProviderNotSupported, val)
		}
	}

	return nil
}

func VariableIntValidator(val string) error {
	if val != "" {
		if _, err := strconv.Atoi(val); err != nil {
			return fmt.Errorf(InvalidIntegerValue, val, err)
		}
	}

	return nil
}

func SizeUnitValidator(val string) error {
	if val != "" {
		if !SliceContains(cliCommon.SizeUnitTypes, strings.ToUpper(val)) {
			return fmt.Errorf(InvalidSizeUnit, val, cliCommon.SizeUnitTypes)
		}
	}

	return nil
}

func FQDNValidator(val string) error {
	if val != "" {
		if !govalidator.IsDNSName(val) {
			return fmt.Errorf(InvalidFqdn, val)
		}
	}

	return nil
}

func RequiredNoCharLimit(val string) error {
	if val != "" {
		if len(val) == 0 {
			return errors.New(NoEmpty)
		}
	}

	return nil
}

func ApiMethodValidator(val string) error {
	if val != "" {
		if !SliceContains(cliCommon.HTTPMethodTypes, strings.ToLower(val)) {
			return fmt.Errorf(InvalidMethodType, val, cliCommon.HTTPMethodTypes)
		}
	}

	return nil
}

func MethodTypeValidator(val string) error {
	if val != "" {
		if !SliceContains(common.FunctionTypes, strings.ToLower(val)) {
			return fmt.Errorf(InvalidApiMethodType, val, common.FunctionTypes)
		}
	}

	return nil
}

func CodeTypeValidator(val string) error {
	var types = constants.CodeTypes
	if val != "" {
		if !SliceContains(types, strings.ToLower(val)) {
			return fmt.Errorf(InvalidCodeType, val, types)
		}
	}

	return nil
}

func BucketTypeValidator(val string) error {
	if val != "" {
		if !SliceContains(common.BucketTypes, val) {
			return fmt.Errorf(InvalidBucketType, val, common.BucketTypes)
		}
	}

	return nil
}

func VariableSizeValidator(val string) error {
	if val != "" {
		if !IsAny(val, IsInt, IsBytes) {
			return fmt.Errorf(InvalidSize, val)
		}
	}

	return nil
}
