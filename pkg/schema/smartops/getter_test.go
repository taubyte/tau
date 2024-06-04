package smartops_test

import (
	"fmt"
	"runtime"
	"testing"

	internal "github.com/taubyte/tau/pkg/schema/internal/test"
	"github.com/taubyte/tau/pkg/schema/smartops"
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

func assertSmartops1(t *testing.T, getter smartops.Getter) {
	eql(t, [][]any{
		{getter.Id(), "smartops1ID"},
		{getter.Name(), "test_smartops1"},
		{getter.Description(), "verifies node has GPU"},
		{getter.Tags(), []string{"smart_tag_1", "smart_tag_2"}},
		{getter.Source(), "."},
		{getter.Timeout(), "6m40s"},
		{getter.Memory(), "16MB"},
		{getter.Call(), "ping1"},
		{getter.Application(), ""},
	})
}

func assertSmartops2(t *testing.T, getter smartops.Getter) {
	eql(t, [][]any{
		{getter.Id(), "smartops2ID"},
		{getter.Name(), "test_smartops2"},
		{getter.Description(), "verifies user is on a specific continent"},
		{getter.Tags(), []string{"smart_tag_3", "smart_tag_4"}},
		{getter.Source(), "library/test_library2"},
		{getter.Timeout(), "5m"},
		{getter.Memory(), "64MB"},
		{getter.Call(), "ping2"},
		{getter.Application(), "test_app1"},
	})
}

func TestGet(t *testing.T) {
	project, err := internal.NewProjectReadOnly()
	assert.NilError(t, err)

	smart, err := project.SmartOps("test_smartops1", "")
	assert.NilError(t, err)

	assertSmartops1(t, smart.Get())

	smart, err = project.SmartOps("test_smartops2", "test_app1")
	assert.NilError(t, err)

	assertSmartops2(t, smart.Get())
}
