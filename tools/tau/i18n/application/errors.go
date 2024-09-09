package applicationI18n

import (
	"fmt"
)

const (
	selectPromptFailed = "selecting an application prompt failed with: %s"
)

func SelectPromptFailed(err error) error {
	return fmt.Errorf(selectPromptFailed, err)
}
