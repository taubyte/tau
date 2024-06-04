package website_test

import (
	"fmt"
	"runtime"
	"testing"

	internal "github.com/taubyte/tau/pkg/schema/internal/test"
	"github.com/taubyte/tau/pkg/schema/website"
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

func assertWebsite1(t *testing.T, getter website.Getter) {
	provider, id, fullname := getter.Git()
	eql(t, [][]any{
		{getter.Id(), "website1ID"},
		{getter.Name(), "test_website1"},
		{getter.Description(), "a simple photo booth"},
		{getter.Tags(), []string{"website_tag_1", "website_tag_2"}},
		{getter.Domains(), []string{"test_domain1"}},
		{getter.Paths(), []string{"/photos"}},
		{getter.Branch(), "main"},
		{provider, "github"},
		{id, "111111111"},
		{fullname, "taubyte-test/photo_booth"},
		{getter.Application(), ""},
		{len(getter.SmartOps()), 0},
	})
}

func assertWebsite2(t *testing.T, getter website.Getter) {
	provider, id, fullname := getter.Git()
	eql(t, [][]any{
		{getter.Id(), "website2ID"},
		{getter.Name(), "test_website2"},
		{getter.Description(), "my portfolio"},
		{getter.Tags(), []string{"website_tag_3", "website_tag_4"}},
		{getter.Domains(), []string{"test_domain2"}},
		{getter.Paths(), []string{"/portfolio"}},
		{getter.Branch(), "main"},
		{provider, "github"},
		{id, "222222222"},
		{fullname, "taubyte-test/portfolio"},
		{getter.Application(), "test_app1"},
		{len(getter.SmartOps()), 0},
	})
}

func TestGet(t *testing.T) {
	project, err := internal.NewProjectReadOnly()
	assert.NilError(t, err)

	web, err := project.Website("test_website1", "")
	assert.NilError(t, err)

	assertWebsite1(t, web.Get())

	web, err = project.Website("test_website2", "test_app1")
	assert.NilError(t, err)

	assertWebsite2(t, web.Get())
}
