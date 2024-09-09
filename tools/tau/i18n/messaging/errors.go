package messagingI18n

import "fmt"

const (
	selectPromptFailed = "selecting a messaging prompt failed with: %s"
)

func SelectPromptFailed(err error) error {
	return fmt.Errorf(selectPromptFailed, err)
}
