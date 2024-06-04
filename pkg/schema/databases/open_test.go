package databases_test

import (
	"testing"

	"github.com/taubyte/tau/pkg/schema/databases"
	internal "github.com/taubyte/tau/pkg/schema/internal/test"
	"gotest.tools/v3/assert"
)

func TestOpenErrors(t *testing.T) {
	seer, err := internal.NewSeer()
	assert.NilError(t, err)

	_, err = databases.Open(seer, "", "")
	assert.ErrorContains(t, err, "on database ``; name is empty")

	_, err = databases.Open(nil, "test_database1", "")
	assert.ErrorContains(t, err, "on database `test_database1`; seer is nil")
}
