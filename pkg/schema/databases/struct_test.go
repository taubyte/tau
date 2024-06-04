package databases_test

import (
	"testing"

	internal "github.com/taubyte/tau/pkg/schema/internal/test"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"gotest.tools/v3/assert"
)

func TestStructBasic(t *testing.T) {
	project, err := internal.NewProjectEmpty()
	assert.NilError(t, err)

	db, err := project.Database("test_database", "")
	assert.NilError(t, err)

	err = db.SetWithStruct(true, &structureSpec.Database{
		Id:          "database1ID",
		Description: "a database for users",
		Tags:        []string{"database_tag_1", "database_tag_2"},
		Match:       "/users",
		Regex:       true,
		Local:       false,
		Key:         "123456",
		Min:         15,
		Max:         30,
		Size:        3412331123213,
		SmartOps:    []string{},
	})
	assert.NilError(t, err)
}

func TestStructError(t *testing.T) {
	project, err := internal.NewProjectEmpty()
	assert.NilError(t, err)

	db, err := project.Database("test_database1", "")
	assert.NilError(t, err)

	err = db.SetWithStruct(true, nil)
	assert.ErrorContains(t, err, "nil pointer")
}
