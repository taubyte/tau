package databases_test

import (
	"fmt"
	"runtime"
	"testing"

	"github.com/taubyte/tau/pkg/schema/databases"
	internal "github.com/taubyte/tau/pkg/schema/internal/test"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

func eql(t *testing.T, a [][]any) {
	_, file, line, _ := runtime.Caller(2)
	for idx, pair := range a {
		switch pair[0].(type) {
		case []string:
			comp := cmp.DeepEqual(pair[0], pair[1])
			assert.Check(t, comp, fmt.Sprintf("item(%d): %s:%d", idx, file, line))
		default:
			assert.Equal(t, pair[0], pair[1], fmt.Sprintf("item(%d): %s:%d", idx, file, line))
		}
	}
}

func assertDatabase1(t *testing.T, getter databases.Getter) {
	eql(t, [][]any{
		{getter.Id(), "database1ID"},
		{getter.Name(), "test_database1"},
		{getter.Description(), "a database for users"},
		{getter.Tags(), []string{"database_tag_1", "database_tag_2"}},
		{getter.Match(), "/users"},
		{getter.Regex(), true},
		{getter.Local(), false},
		{getter.Min(), 15},
		{getter.Max(), 30},
		{getter.Application(), ""},
		{len(getter.SmartOps()), 0},
	})
}

func assertDatabase2(t *testing.T, getter databases.Getter) {
	eql(t, [][]any{
		{getter.Id(), "database2ID"},
		{getter.Name(), "test_database2"},
		{getter.Description(), "a profiles database"},
		{getter.Tags(), []string{"database_tag_3", "database_tag_4"}},
		{getter.Match(), "profiles"},
		{getter.Regex(), false},
		{getter.Local(), true},
		{getter.Min(), 42},
		{getter.Max(), 601},
		{getter.Application(), "test_app1"},
		{len(getter.SmartOps()), 0},
	})
}

func TestGet(t *testing.T) {
	project, err := internal.NewProjectReadOnly()
	assert.NilError(t, err)

	db, err := project.Database("test_database1", "")
	assert.NilError(t, err)

	assertDatabase1(t, db.Get())

	db, err = project.Database("test_database2", "test_app1")
	assert.NilError(t, err)

	assertDatabase2(t, db.Get())
}
