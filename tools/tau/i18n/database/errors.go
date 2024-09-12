package databaseI18n

import "fmt"

const (
	selectPromptFailed = "selecting a database prompt failed with: %s"
)

func SelectPromptFailed(err error) error {
	return fmt.Errorf(selectPromptFailed, err)
}
