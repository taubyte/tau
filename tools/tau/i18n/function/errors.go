package functionI18n

import "fmt"

const (
	selectPromptFailed = "selecting a function prompt failed with: %s"
)

func SelectPromptFailed(err error) error {
	return fmt.Errorf(selectPromptFailed, err)
}
