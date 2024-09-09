package smartopsI18n

import "fmt"

const (
	selectPromptFailed = "selecting a smartops prompt failed with: %s"
)

func SelectPromptFailed(err error) error {
	return fmt.Errorf(selectPromptFailed, err)
}
