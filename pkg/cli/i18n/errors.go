package i18n

import (
	"errors"
	"fmt"
)

const (
	appCrashed      = "command failed with: %s"
	appCreateFailed = "creating new app failed with: %s"

	gettingCwdFailed = "getting current working directory failed with: %s"
	doesNotExist     = "%s: `%s` does not exist"
	invalidParameter = "parameter `%s`: %v is invalid, %s"
)

func AppCrashed(err error) error {
	return fmt.Errorf(appCrashed, err)
}

func AppCreateFailed(err error) error {
	return fmt.Errorf(appCreateFailed, err)
}

func GettingCwdFailed(err error) error {
	return fmt.Errorf(gettingCwdFailed, err)
}

func ErrorDoesNotExist(prefix, name string) error {
	return fmt.Errorf(doesNotExist, prefix, name)
}

func ErrorTime0Invalid() error {
	return errors.New("0 time is invalid")
}

func ErrorInvalidParameter(value any, paramName, cause string) error {
	return fmt.Errorf(invalidParameter, paramName, value, cause)
}
