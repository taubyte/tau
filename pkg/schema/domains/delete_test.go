package domains_test

import (
	"testing"

	"github.com/taubyte/tau/pkg/schema/domains"
	internal "github.com/taubyte/tau/pkg/schema/internal/test"
	"gotest.tools/v3/assert"
)

func TestDeleteBasic(t *testing.T) {
	project, close, err := internal.NewProjectCopy()
	assert.NilError(t, err)
	defer close()

	dom, err := project.Domain("test_domain2", "test_app1")
	assert.NilError(t, err)

	assertDomain2(t, dom.Get())

	err = dom.Delete()
	assert.NilError(t, err)
	internal.AssertEmpty(t,
		dom.Get().Id(),
		dom.Get().Name(),
		dom.Get().Description(),
		dom.Get().Tags(),
		dom.Get().FQDN(),
		dom.Get().UseCertificate(),
		dom.Get().Key(),
		dom.Get().Cert(),
	)

	local, _ := project.Get().Domains("test_app1")
	assert.Equal(t, len(local), 0)

	dom, err = project.Domain("test_domain2", "test_app1")
	assert.NilError(t, err)

	assert.Equal(t, dom.Get().Name(), "test_domain2")
	internal.AssertEmpty(t,
		dom.Get().Id(),
		dom.Get().Description(),
		dom.Get().Tags(),
		dom.Get().FQDN(),
		dom.Get().UseCertificate(),
		dom.Get().Key(),
		dom.Get().Cert(),
	)
}

func TestDeleteAttributes(t *testing.T) {
	project, close, err := internal.NewProjectCopy()
	assert.NilError(t, err)
	defer close()

	dom, err := project.Domain("test_domain1", "")
	assert.NilError(t, err)

	assertDomain1(t, dom.Get())

	err = dom.Delete("description", "fqdn", "certificate")
	assert.NilError(t, err)

	assertion := func(_dom domains.Domain) {
		eql(t, [][]any{
			{_dom.Get().Id(), "domain1ID"},
			{_dom.Get().Name(), "test_domain1"},
			{_dom.Get().Description(), ""},
			{_dom.Get().Tags(), []string{"domain_tag_1", "domain_tag_2"}},
			{_dom.Get().FQDN(), ""},
			{_dom.Get().UseCertificate(), false},
			{_dom.Get().Key(), ""},
			{_dom.Get().Cert(), ""},
			{_dom.Get().Application(), ""},
		})
	}
	assertion(dom)

	// Re-open
	dom, err = project.Domain("test_domain1", "")
	assert.NilError(t, err)

	assert.Equal(t, dom.Get().Id(), "domain1ID")
	assert.Equal(t, dom.Get().Name(), "test_domain1")
	assertion(dom)
}
