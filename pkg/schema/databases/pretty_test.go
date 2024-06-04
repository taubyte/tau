package databases_test

import (
	"testing"

	internal "github.com/taubyte/tau/pkg/schema/internal/test"
	"gotest.tools/v3/assert"
)

func TestPretty(t *testing.T) {
	project, err := internal.NewProjectReadOnly()
	assert.NilError(t, err)

	db, err := project.Database("test_database1", "")
	assert.NilError(t, err)

	assert.DeepEqual(t, db.Prettify(nil), map[string]interface{}{
		"Description":     "a database for users",
		"Encryption-Type": "",
		"Id":              "database1ID",
		"Local":           false,
		"Match":           "/users",
		"Max":             30,
		"Min":             15,
		"Name":            "test_database1",
		"Regex":           true,
		"Secret":          false,
		"Size":            "5GB",
		"Tags":            []string{"database_tag_1", "database_tag_2"},
	})
}
