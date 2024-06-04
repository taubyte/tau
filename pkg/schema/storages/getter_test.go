package storages_test

import (
	"fmt"
	"runtime"
	"testing"

	internal "github.com/taubyte/tau/pkg/schema/internal/test"
	"github.com/taubyte/tau/pkg/schema/storages"
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

func assertStorage1(t *testing.T, getter storages.Getter) {
	eql(t, [][]any{
		{getter.Id(), "storage1ID"},
		{getter.Name(), "test_storage1"},
		{getter.Description(), "a streaming storage"},
		{getter.Tags(), []string{"storage_tag_1", "storage_tag_2"}},
		{getter.Match(), "photos"},
		{getter.Regex(), true},
		{getter.Public(), false},
		{getter.Versioning(), false},
		{getter.TTL(), "5m"},
		{getter.Size(), "30GB"},
		{getter.Type(), "streaming"},
		{getter.Application(), ""},
		{len(getter.SmartOps()), 0},
	})
}

func assertStorage2(t *testing.T, getter storages.Getter) {
	eql(t, [][]any{
		{getter.Id(), "storage2ID"},
		{getter.Name(), "test_storage2"},
		{getter.Description(), "an object storage"},
		{getter.Tags(), []string{"storage_tag_3", "storage_tag_4"}},
		{getter.Match(), "users"},
		{getter.Regex(), false},
		{getter.Public(), true},
		{getter.Versioning(), true},
		{getter.TTL(), ""},
		{getter.Size(), "50GB"},
		{getter.Type(), "object"},
		{getter.Application(), "test_app1"},
		{len(getter.SmartOps()), 0},
	})
}

func TestGet(t *testing.T) {
	project, err := internal.NewProjectReadOnly()
	assert.NilError(t, err)

	stg, err := project.Storage("test_storage1", "")
	assert.NilError(t, err)

	assertStorage1(t, stg.Get())

	stg, err = project.Storage("test_storage2", "test_app1")
	assert.NilError(t, err)

	assertStorage2(t, stg.Get())
}
