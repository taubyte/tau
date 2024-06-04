package databases_test

import (
	"testing"

	"github.com/taubyte/tau/pkg/schema/databases"
	internal "github.com/taubyte/tau/pkg/schema/internal/test"
	"gotest.tools/v3/assert"
)

func TestDeleteBasic(t *testing.T) {
	project, close, err := internal.NewProjectCopy()
	assert.NilError(t, err)
	defer close()

	db, err := project.Database("test_database2", "test_app1")
	assert.NilError(t, err)

	assertDatabase2(t, db.Get())

	err = db.Delete()
	assert.NilError(t, err)
	internal.AssertEmpty(t,
		db.Get().Id(),
		db.Get().Name(),
		db.Get().Description(),
		db.Get().Tags(),
		db.Get().Match(),
		db.Get().Regex(),
		db.Get().Local(),
		db.Get().Min(),
		db.Get().Max(),
	)

	local, _ := project.Get().Databases("test_app1")
	assert.Equal(t, len(local), 0)

	db, err = project.Database("test_database2", "test_app1")
	assert.NilError(t, err)

	assert.Equal(t, db.Get().Name(), "test_database2")
	internal.AssertEmpty(t,
		db.Get().Id(),
		db.Get().Description(),
		db.Get().Tags(),
		db.Get().Match(),
		db.Get().Regex(),
		db.Get().Local(),
		db.Get().Min(),
		db.Get().Max(),
	)
}

func TestDeleteAttributes(t *testing.T) {
	project, close, err := internal.NewProjectCopy()
	assert.NilError(t, err)
	defer close()

	db, err := project.Database("test_database1", "")
	assert.NilError(t, err)

	assertDatabase1(t, db.Get())

	err = db.Delete("description", "match", "replicas")
	assert.NilError(t, err)

	assertion := func(_db databases.Database) {
		eql(t, [][]any{
			{_db.Get().Id(), "database1ID"},
			{_db.Get().Name(), "test_database1"},
			{_db.Get().Description(), ""},
			{_db.Get().Tags(), []string{"database_tag_1", "database_tag_2"}},
			{_db.Get().Match(), ""},
			{_db.Get().Regex(), true},
			{_db.Get().Local(), false},
			{_db.Get().Min(), 0},
			{_db.Get().Max(), 0},
			{_db.Get().Application(), ""},
		})
	}
	assertion(db)

	// Re-open
	db, err = project.Database("test_database1", "")
	assert.NilError(t, err)

	assert.Equal(t, db.Get().Id(), "database1ID")
	assert.Equal(t, db.Get().Name(), "test_database1")
	assertion(db)
}
