package website_test

import (
	"testing"

	internal "github.com/taubyte/tau/pkg/schema/internal/test"
	"github.com/taubyte/tau/pkg/schema/website"
	"gotest.tools/v3/assert"
)

func TestSetBasic(t *testing.T) {
	project, close, err := internal.NewProjectCopy()
	assert.NilError(t, err)
	defer close()

	web, err := project.Website("test_website1", "")
	assert.NilError(t, err)

	assertWebsite1(t, web.Get())

	var (
		id                 = "website3ID"
		description        = "this is test web 3"
		tags               = []string{"web_tag_5", "web_tag_6"}
		domains            = []string{"otherTestDomain"}
		paths              = []string{"/"}
		branch             = "dreamland"
		gitProvider        = "github"
		repositoryID       = "444444444"
		repositoryFullName = "taubyte_test/forty_four"
	)

	err = web.Set(true,
		website.Id(id),
		website.Description(description),
		website.Tags(tags),
		website.Domains(domains),
		website.Paths(paths),
		website.Branch(branch),
		website.Github(repositoryID, repositoryFullName),
	)
	assert.NilError(t, err)

	assertion := func(_web website.Website) {
		provider, repoId, fullname := web.Get().Git()
		eql(t, [][]any{
			{_web.Get().Id(), id},
			{_web.Get().Name(), "test_website1"},
			{_web.Get().Description(), description},
			{_web.Get().Tags(), tags},
			{_web.Get().Domains(), domains},
			{_web.Get().Paths(), paths},
			{_web.Get().Branch(), branch},
			{provider, gitProvider},
			{repoId, repositoryID},
			{fullname, repositoryFullName},
			{_web.Get().Application(), ""},
		})
	}
	assertion(web)

	web, err = project.Website("test_website1", "")
	assert.NilError(t, err)

	assertion(web)
}

func TestSetInApp(t *testing.T) {
	project, close, err := internal.NewProjectCopy()
	assert.NilError(t, err)
	defer close()

	web, err := project.Website("test_website2", "test_app1")
	assert.NilError(t, err)

	assertWebsite2(t, web.Get())

	var (
		id                 = "website3ID"
		description        = "this is test web 3"
		tags               = []string{"web_tag_5", "web_tag_6"}
		domains            = []string{"otherTestDomain"}
		paths              = []string{"/"}
		branch             = "dreamland"
		gitProvider        = "github"
		repositoryID       = "444444444"
		repositoryFullName = "taubyte_test/forty_four"
	)

	err = web.Set(true,
		website.Id(id),
		website.Description(description),
		website.Tags(tags),
		website.Domains(domains),
		website.Paths(paths),
		website.Branch(branch),
		website.Github(repositoryID, repositoryFullName),
		website.SmartOps([]string{"smart1"}),
	)
	assert.NilError(t, err)

	assertion := func(_web website.Website) {
		provider, repoId, fullname := web.Get().Git()
		eql(t, [][]any{
			{_web.Get().Id(), id},
			{_web.Get().Name(), "test_website2"},
			{_web.Get().Description(), description},
			{_web.Get().Tags(), tags},
			{_web.Get().Domains(), domains},
			{_web.Get().Paths(), paths},
			{_web.Get().Branch(), branch},
			{provider, gitProvider},
			{repoId, repositoryID},
			{fullname, repositoryFullName},
			{_web.Get().Application(), "test_app1"},
			{_web.Get().SmartOps(), []string{"smart1"}},
		})
	}
	assertion(web)

	web, err = project.Website("test_website2", "test_app1")
	assert.NilError(t, err)

	assertion(web)
}
