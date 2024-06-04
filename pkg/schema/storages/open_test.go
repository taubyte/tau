package storages_test

import (
	"testing"

	internal "github.com/taubyte/tau/pkg/schema/internal/test"
	"github.com/taubyte/tau/pkg/schema/storages"
	"gotest.tools/v3/assert"
)

func TestOpenErrors(t *testing.T) {
	seer, err := internal.NewSeer()
	assert.NilError(t, err)

	_, err = storages.Open(seer, "", "")
	assert.ErrorContains(t, err, "on storage ``; name is empty")

	_, err = storages.Open(nil, "test_storage1", "")
	assert.ErrorContains(t, err, "on storage `test_storage1`; seer is nil")
}
