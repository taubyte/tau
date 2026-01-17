package databases

import (
	"testing"

	"github.com/taubyte/tau/pkg/schema/basic"
	seer "github.com/taubyte/tau/pkg/yaseer"
	"gotest.tools/v3/assert"
)

func TestTypes(t *testing.T) {
	db := &database{
		Resource: &basic.Resource{},
		seer:     &seer.Seer{},
		name:     "db1",
	}

	assert.Equal(t, db.Name(), "db1")
	assert.Equal(t, db.AppName(), "")

	err := db.WrapError("failed: %s", "test error")
	assert.ErrorContains(t, err, "on database `db1`; failed: test error")
}
