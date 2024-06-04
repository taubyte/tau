package storages_test

import (
	"testing"

	internal "github.com/taubyte/tau/pkg/schema/internal/test"
	"github.com/taubyte/tau/pkg/schema/storages"
	"gotest.tools/v3/assert"
)

func TestDeleteBasic(t *testing.T) {
	project, close, err := internal.NewProjectCopy()
	assert.NilError(t, err)
	defer close()

	stg, err := project.Storage("test_storage2", "test_app1")
	assert.NilError(t, err)

	assertStorage2(t, stg.Get())

	err = stg.Delete()
	assert.NilError(t, err)
	internal.AssertEmpty(t,
		stg.Get().Id(),
		stg.Get().Name(),
		stg.Get().Description(),
		stg.Get().Tags(),
		stg.Get().Match(),
		stg.Get().Regex(),
		stg.Get().Public(),
		stg.Get().Versioning(),
		stg.Get().TTL(),
		stg.Get().Size(),
		stg.Get().Type(),
		stg.Get().SmartOps(),
	)

	local, _ := project.Get().Storages("test_app1")
	assert.Equal(t, len(local), 0)

	stg, err = project.Storage("test_storage2", "test_app1")
	assert.NilError(t, err)

	assert.Equal(t, stg.Get().Name(), "test_storage2")
	internal.AssertEmpty(t,
		stg.Get().Id(),
		stg.Get().Description(),
		stg.Get().Tags(),
		stg.Get().Match(),
		stg.Get().Regex(),
		stg.Get().Public(),
		stg.Get().Versioning(),
		stg.Get().TTL(),
		stg.Get().Size(),
		stg.Get().Type(),
		stg.Get().SmartOps(),
	)
}

func TestDeleteAttributes(t *testing.T) {
	project, close, err := internal.NewProjectCopy()
	assert.NilError(t, err)
	defer close()

	stg, err := project.Storage("test_storage1", "")
	assert.NilError(t, err)

	assertStorage1(t, stg.Get())

	err = stg.Delete("description", "streaming")
	assert.NilError(t, err)

	assertion := func(_stg storages.Storage) {
		eql(t, [][]any{
			{_stg.Get().Id(), "storage1ID"},
			{_stg.Get().Name(), "test_storage1"},
			{_stg.Get().Description(), ""},
			{_stg.Get().Tags(), []string{"storage_tag_1", "storage_tag_2"}},
			{_stg.Get().Public(), false},
			{_stg.Get().Regex(), true},
			{_stg.Get().Versioning(), false},
			{_stg.Get().Match(), "photos"},
			{_stg.Get().TTL(), ""},
			{_stg.Get().Size(), ""},
			{_stg.Get().Type(), ""},
			{len(_stg.Get().SmartOps()), 0},
			{_stg.Get().Application(), ""},
		})
	}
	assertion(stg)

	// Re-open
	stg, err = project.Storage("test_storage1", "")
	assert.NilError(t, err)

	assert.Equal(t, stg.Get().Id(), "storage1ID")
	assert.Equal(t, stg.Get().Name(), "test_storage1")
	assertion(stg)
}
