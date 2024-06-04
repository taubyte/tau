package storages_test

import (
	"testing"
	"time"

	"github.com/alecthomas/units"
	internal "github.com/taubyte/tau/pkg/schema/internal/test"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"gotest.tools/v3/assert"
)

func TestStructObject(t *testing.T) {
	project, err := internal.NewProjectEmpty()
	assert.NilError(t, err)

	stg, err := project.Storage("test_storage2", "test_app1")
	assert.NilError(t, err)

	err = stg.SetWithStruct(true, &structureSpec.Storage{
		Id:          "storage2ID",
		Description: "an object storage",
		Tags:        []string{"storage_tag_3", "storage_tag_4"},
		Match:       "users",
		Regex:       false,
		Public:      true,
		Versioning:  true,
		Type:        "object",
		Size:        uint64(50 * units.GB),
	})
	assert.NilError(t, err)

	assertStorage2(t, stg.Get())

	stg, err = project.Storage("test_storage2", "test_app1")
	assert.NilError(t, err)

	assertStorage2(t, stg.Get())
}

func TestStructStreaming(t *testing.T) {
	project, err := internal.NewProjectEmpty()
	assert.NilError(t, err)

	stg, err := project.Storage("test_storage1", "")
	assert.NilError(t, err)

	err = stg.SetWithStruct(true, &structureSpec.Storage{
		Id:          "storage1ID",
		Description: "a streaming storage",
		Tags:        []string{"storage_tag_1", "storage_tag_2"},
		Match:       "photos",
		Regex:       true,
		Public:      false,
		Type:        "streaming",
		Ttl:         uint64(5 * time.Minute),
		Size:        uint64(30 * units.GB),
	})
	assert.NilError(t, err)

	assertStorage1(t, stg.Get())

	stg, err = project.Storage("test_storage1", "")
	assert.NilError(t, err)

	assertStorage1(t, stg.Get())

	err = stg.SetWithStruct(true, &structureSpec.Storage{
		Type:     "streaming",
		SmartOps: []string{"smart1"},
	})
	assert.NilError(t, err)
	assert.DeepEqual(t, stg.Get().SmartOps(), []string{"smart1"})
}

func TestStructError(t *testing.T) {
	project, err := internal.NewProjectEmpty()
	assert.NilError(t, err)

	stg, err := project.Storage("test_storage1", "")
	assert.NilError(t, err)

	err = stg.SetWithStruct(true, nil)
	assert.ErrorContains(t, err, "nil pointer")

	// Set with no defined type
	err = stg.SetWithStruct(true, &structureSpec.Storage{})
	assert.ErrorContains(t, err, "failed with: Storage type `` not allowed")
}
