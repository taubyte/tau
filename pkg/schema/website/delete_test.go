package website_test

import (
	"testing"

	internal "github.com/taubyte/tau/pkg/schema/internal/test"
	"github.com/taubyte/tau/pkg/schema/website"
	"gotest.tools/v3/assert"
)

func TestDeleteBasic(t *testing.T) {
	project, close, err := internal.NewProjectCopy()
	assert.NilError(t, err)
	defer close()

	web, err := project.Website("test_website2", "test_app1")
	assert.NilError(t, err)

	assertWebsite2(t, web.Get())

	err = web.Delete()
	assert.NilError(t, err)

	provider, id, fullname := web.Get().Git()
	internal.AssertEmpty(t,
		web.Get().Id(),
		web.Get().Name(),
		web.Get().Description(),
		web.Get().Tags(),
		web.Get().Domains(),
		web.Get().Paths(),
		web.Get().Branch(),
		provider,
		id,
		fullname,
	)

	local, _ := project.Get().Websites("test_app1")
	assert.Equal(t, len(local), 0)

	web, err = project.Website("test_website2", "test_app1")
	assert.NilError(t, err)
	assert.Equal(t, web.Get().Name(), "test_website2")

	provider, id, fullname = web.Get().Git()
	internal.AssertEmpty(t,
		web.Get().Id(),
		web.Get().Description(),
		web.Get().Tags(),
		web.Get().Domains(),
		web.Get().Paths(),
		web.Get().Branch(),
		provider,
		id,
		fullname,
	)
}

func TestDeleteAttributes(t *testing.T) {
	project, close, err := internal.NewProjectCopy()
	assert.NilError(t, err)
	defer close()

	web, err := project.Website("test_website1", "")
	assert.NilError(t, err)

	assertWebsite1(t, web.Get())

	err = web.Delete("description", "domains")
	assert.NilError(t, err)

	assertion := func(_web website.Website) {
		provider, id, fullname := _web.Get().Git()
		eql(t, [][]any{
			{_web.Get().Id(), "website1ID"},
			{_web.Get().Name(), "test_website1"},
			{_web.Get().Description(), ""},
			{_web.Get().Tags(), []string{"website_tag_1", "website_tag_2"}},
			{len(_web.Get().Domains()), 0},
			{_web.Get().Paths(), []string{"/photos"}},
			{provider, "github"},
			{id, "111111111"},
			{fullname, "taubyte-test/photo_booth"},
			{_web.Get().Application(), ""},
		})
	}
	assertion(web)

	// Re-open
	web, err = project.Website("test_website1", "")
	assert.NilError(t, err)
	assertion(web)
}
