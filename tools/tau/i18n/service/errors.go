package serviceI18n

import "fmt"

const (
	selectPromptFailed = "selecting a service prompt failed with: %s"
)

func SelectPromptFailed(err error) error {
	return fmt.Errorf(selectPromptFailed, err)
}
