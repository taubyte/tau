package cloudI18n

import (
	"errors"
	"fmt"
)

const (
	flagError = "only set one flag corresponding to a cloud"
)

func FlagError() error {
	return errors.New(flagError)
}

func ErrorUnknownCloud(cloud string) error {
	return fmt.Errorf("unknown cloud `%s`", cloud)
}
