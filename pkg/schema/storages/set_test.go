package storages_test

import (
	"testing"

	internal "github.com/taubyte/tau/pkg/schema/internal/test"
	"github.com/taubyte/tau/pkg/schema/storages"
	"gotest.tools/v3/assert"
)

func TestSetBasic(t *testing.T) {
	project, close, err := internal.NewProjectCopy()
	assert.NilError(t, err)
	defer close()

	stg, err := project.Storage("test_storage1", "")
	assert.NilError(t, err)

	assertStorage1(t, stg.Get())

	var (
		id          = "storage3ID"
		description = "this is test stg 3"
		tags        = []string{"stg_tag_5", "stg_tag_6"}
		match       = "networks"
		regex       = false
		public      = true
		ttl         = "15m"
		size        = "40MB"
		smartOps    = []string{"smart1"}
	)

	err = stg.Set(true,
		storages.Id(id),
		storages.Description(description),
		storages.Tags(tags),
		storages.Match(match),
		storages.Regex(regex),
		storages.Public(public),
		storages.Streaming(ttl, size),
		storages.SmartOps(smartOps),
	)
	assert.NilError(t, err)

	assertion := func(_stg storages.Storage) {
		eql(t, [][]any{
			{_stg.Get().Id(), id},
			{_stg.Get().Name(), "test_storage1"},
			{_stg.Get().Description(), description},
			{_stg.Get().Tags(), tags},
			{_stg.Get().Match(), match},
			{_stg.Get().Regex(), regex},
			{_stg.Get().Public(), public},
			{_stg.Get().Versioning(), false},
			{_stg.Get().TTL(), ttl},
			{_stg.Get().Size(), size},
			{_stg.Get().SmartOps(), smartOps},
			{_stg.Get().Application(), ""},
		})
	}
	assertion(stg)

	stg, err = project.Storage("test_storage1", "")
	assert.NilError(t, err)

	assertion(stg)
}

func TestSetInApp(t *testing.T) {
	project, close, err := internal.NewProjectCopy()
	assert.NilError(t, err)
	defer close()

	stg, err := project.Storage("test_storage2", "test_app1")
	assert.NilError(t, err)

	assertStorage2(t, stg.Get())

	var (
		id          = "storage3ID"
		description = "this is test stg 3"
		tags        = []string{"stg_tag_5", "stg_tag_6"}
		match       = "^[0-9]"
		regex       = true
		public      = false
		versioning  = false
		size        = "20TB"
	)

	err = stg.Set(true,
		storages.Id(id),
		storages.Description(description),
		storages.Tags(tags),
		storages.Match(match),
		storages.Regex(regex),
		storages.Public(public),
		storages.Object(versioning, size),
	)
	assert.NilError(t, err)

	assertion := func(_stg storages.Storage) {
		eql(t, [][]any{
			{_stg.Get().Id(), id},
			{_stg.Get().Name(), "test_storage2"},
			{_stg.Get().Description(), description},
			{_stg.Get().Tags(), tags},
			{_stg.Get().Match(), match},
			{_stg.Get().Regex(), regex},
			{_stg.Get().Public(), public},
			{_stg.Get().Versioning(), versioning},
			{_stg.Get().TTL(), ""},
			{_stg.Get().Size(), size},
			{len(_stg.Get().SmartOps()), 0},
			{_stg.Get().Application(), "test_app1"},
		})
	}
	assertion(stg)

	stg, err = project.Storage("test_storage2", "test_app1")
	assert.NilError(t, err)

	assertion(stg)
}
