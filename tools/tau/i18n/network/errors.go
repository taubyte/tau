package networkI18n

import (
	"errors"
	"fmt"
)

const (
	flagError = "only set one flag corresponding to a network"
)

func FlagError() error {
	return errors.New(flagError)
}

func ErrorUnknownNetwork(network string) error {
	return fmt.Errorf("unknown network `%s`", network)
}
