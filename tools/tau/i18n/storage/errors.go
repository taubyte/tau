package storageI18n

import "fmt"

const (
	selectPromptFailed = "selecting a storage prompt failed with: %s"
	selectBucketFailed = "selecting a bucket type failed with: %s"
)

func SelectPromptFailed(err error) error {
	return fmt.Errorf(selectPromptFailed, err)
}

func SelectBucketFailed(err error) error {
	return fmt.Errorf(selectBucketFailed, err)
}
