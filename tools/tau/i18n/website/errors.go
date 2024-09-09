package websiteI18n

import (
	"errors"
	"fmt"
)

const (
	selectPromptFailed = "selecting a website prompt failed with: %s"
)

var (
	ErrorAlreadyCloned = errors.New("already cloned")
)

func SelectPromptFailed(err error) error {
	return fmt.Errorf(selectPromptFailed, err)
}
