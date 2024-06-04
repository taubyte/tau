package domains_test

import (
	"testing"

	"github.com/taubyte/tau/pkg/schema/domains"
	internal "github.com/taubyte/tau/pkg/schema/internal/test"
	"gotest.tools/v3/assert"
)

func TestSetBasic(t *testing.T) {
	project, close, err := internal.NewProjectCopy()
	assert.NilError(t, err)
	defer close()

	dom, err := project.Domain("test_domain1", "")
	assert.NilError(t, err)

	assertDomain1(t, dom.Get())

	var (
		id             = "domain3ID"
		description    = "this is test dom 3"
		tags           = []string{"dom_tag_5", "dom_tag_6"}
		fqdn           = "test.computers.com"
		useCertificate = false
	)

	err = dom.Set(true,
		domains.Id(id),
		domains.Description(description),
		domains.Tags(tags),
		domains.FQDN(fqdn),
		domains.UseCertificate(useCertificate),
	)
	assert.NilError(t, err)

	assertion := func(_dom domains.Domain) {
		eql(t, [][]any{
			{_dom.Get().Id(), id},
			{_dom.Get().Name(), "test_domain1"},
			{_dom.Get().Description(), description},
			{_dom.Get().Tags(), tags},
			{_dom.Get().FQDN(), fqdn},
			{_dom.Get().UseCertificate(), useCertificate},
			{_dom.Get().Cert(), ""},
			{_dom.Get().Key(), ""},
			{_dom.Get().Application(), ""},
		})
	}
	assertion(dom)

	dom, err = project.Domain("test_domain1", "")
	assert.NilError(t, err)

	assertion(dom)
}

func TestSetInApp(t *testing.T) {
	project, close, err := internal.NewProjectCopy()
	assert.NilError(t, err)
	defer close()

	dom, err := project.Domain("test_domain2", "test_app1")
	assert.NilError(t, err)

	assertDomain2(t, dom.Get())

	var (
		id          = "domain3ID"
		description = "this is test dom 3"
		tags        = []string{"dom_tag_5", "dom_tag_6"}
		fqdn        = "test.computers.com"
		cert        = "certificate1"
		key         = "key1"
	)

	err = dom.Set(true,
		domains.Id(id),
		domains.Description(description),
		domains.Tags(tags),
		domains.FQDN(fqdn),
		domains.Cert(cert),
		domains.Key(key),
	)
	assert.NilError(t, err)

	assertion := func(_dom domains.Domain) {
		eql(t, [][]any{
			{_dom.Get().Id(), id},
			{_dom.Get().Name(), "test_domain2"},
			{_dom.Get().Description(), description},
			{_dom.Get().Tags(), tags},
			{_dom.Get().FQDN(), fqdn},
			{_dom.Get().UseCertificate(), true},
			{_dom.Get().Cert(), cert},
			{_dom.Get().Key(), key},
			{_dom.Get().Application(), "test_app1"},
		})
	}
	assertion(dom)

	dom, err = project.Domain("test_domain2", "test_app1")
	assert.NilError(t, err)

	assertion(dom)
}

func TestSetMisc(t *testing.T) {
	project, err := internal.NewProjectEmpty()
	assert.NilError(t, err)

	dom, err := project.Domain("test_domain", "")
	assert.NilError(t, err)

	err = dom.Set(true, domains.UseCertificate(true))
	assert.NilError(t, err)

	assert.Assert(t, dom.Get().UseCertificate())
}
