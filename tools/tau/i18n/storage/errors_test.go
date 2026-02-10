package storageI18n_test

import (
	"errors"
	"testing"

	storageI18n "github.com/taubyte/tau/tools/tau/i18n/storage"
	"gotest.tools/v3/assert"
)

func TestSelectPromptFailed(t *testing.T) {
	err := storageI18n.SelectPromptFailed(errors.New("bad"))
	assert.ErrorContains(t, err, "storage prompt failed")
}

func TestSelectBucketFailed(t *testing.T) {
	err := storageI18n.SelectBucketFailed(errors.New("bucket err"))
	assert.ErrorContains(t, err, "bucket")
}
