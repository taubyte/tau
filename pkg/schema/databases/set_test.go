package databases_test

import (
	"testing"

	"github.com/taubyte/tau/pkg/schema/databases"
	internal "github.com/taubyte/tau/pkg/schema/internal/test"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

func TestSetBasic(t *testing.T) {
	project, close, err := internal.NewProjectCopy()
	assert.NilError(t, err)
	defer close()

	db, err := project.Database("test_database1", "")
	assert.NilError(t, err)

	assertDatabase1(t, db.Get())

	var (
		id          = "database3ID"
		description = "this is test db 3"
		tags        = []string{"db_tag_5", "db_tag_6"}
		match       = "/test/v1"
		regex       = false
		local       = true
	)

	err = db.Set(true,
		databases.Id(id),
		databases.Description(description),
		databases.Tags(tags),
		databases.Match(match),
		databases.Regex(regex),
		databases.Local(local),
		databases.Replicas(0, 10),
	)
	assert.NilError(t, err)

	assertion := func(_db databases.Database) {
		eql(t, [][]any{
			{_db.Get().Id(), id},
			{_db.Get().Name(), "test_database1"},
			{_db.Get().Description(), description},
			{_db.Get().Tags(), tags},
			{_db.Get().Match(), match},
			{_db.Get().Regex(), regex},
			{_db.Get().Local(), local},
			{_db.Get().Min(), 0},
			{_db.Get().Max(), 10},
			{_db.Get().Application(), ""},
		})
	}
	assertion(db)

	db, err = project.Database("test_database1", "")
	assert.NilError(t, err)

	assertion(db)
}

func TestSetInApp(t *testing.T) {
	project, close, err := internal.NewProjectCopy()
	assert.NilError(t, err)
	defer close()

	db, err := project.Database("test_database2", "test_app1")
	assert.NilError(t, err)

	assertDatabase2(t, db.Get())

	var (
		id          = "database3ID"
		description = "this is test db 3"
		tags        = []string{"db_tag_5", "db_tag_6"}
		match       = "/test/v1"
		regex       = true
		local       = false
	)

	err = db.Set(true,
		databases.Id(id),
		databases.Description(description),
		databases.Tags(tags),
		databases.Match(match),
		databases.Regex(regex),
		databases.Local(local),
		databases.Replicas(0, 10),
	)
	assert.NilError(t, err)

	assertion := func(_db databases.Database) {
		eql(t, [][]any{
			{_db.Get().Id(), id},
			{_db.Get().Name(), "test_database2"},
			{_db.Get().Description(), description},
			{_db.Get().Tags(), tags},
			{_db.Get().Match(), match},
			{_db.Get().Regex(), regex},
			{_db.Get().Local(), local},
			{_db.Get().Min(), 0},
			{_db.Get().Max(), 10},
			{_db.Get().Application(), "test_app1"},
		})
	}
	assertion(db)

	db, err = project.Database("test_database2", "test_app1")
	assert.NilError(t, err)

	assertion(db)
}

func TestSetMisc(t *testing.T) {
	project, err := internal.NewProjectEmpty()
	assert.NilError(t, err)

	db, err := project.Database("test_database", "")
	assert.NilError(t, err)

	err = db.Set(true, databases.Encryption("123456"))
	assert.NilError(t, err)

	key, _ := db.Get().Encryption()
	assert.Equal(t, key, "123456")

	err = db.Set(true, databases.SmartOps([]string{"smart1", "smart2"}))
	assert.NilError(t, err)
	assert.Equal(t, len(db.Get().SmartOps()), 2)

	assert.Check(t, cmp.Panics(func() {
		db.Set(true, databases.Replicas(10, 0))
	}))

}
