package domains_test

import (
	"testing"

	internal "github.com/taubyte/tau/pkg/schema/internal/test"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"gotest.tools/v3/assert"
)

func TestStruct(t *testing.T) {
	project, err := internal.NewProjectEmpty()
	assert.NilError(t, err)

	dom, err := project.Domain("test_domain1", "")
	assert.NilError(t, err)

	err = dom.SetWithStruct(true, &structureSpec.Domain{
		Id:          "domain1ID",
		Description: "a domain for hal computers",
		Tags:        []string{"domain_tag_1", "domain_tag_2"},
		Fqdn:        "hal.computers.com",
		CertType:    "inline",
		CertFile:    "testCert",
		KeyFile:     "testKey",
		SmartOps:    []string{},
	})
	assert.NilError(t, err)

	assertDomain1(t, dom.Get())

	dom, err = project.Domain("test_domain2", "")
	assert.NilError(t, err)

	// Use different cert type
	err = dom.SetWithStruct(true, &structureSpec.Domain{
		Id:          "domain1ID",
		Description: "a domain for hal computers",
		Tags:        []string{"domain_tag_1", "domain_tag_2"},
		Fqdn:        "hal.computers.com",
		CertType:    "other",
		CertFile:    "otherCert",
		KeyFile:     "otherKey",
		SmartOps:    []string{},
	})
	assert.NilError(t, err)

	eql(t, [][]any{
		{dom.Get().Cert(), "otherCert"},
		{dom.Get().Key(), "otherKey"},
		{dom.Get().Type(), "other"},
	})
}

func TestStructError(t *testing.T) {
	project, err := internal.NewProjectEmpty()
	assert.NilError(t, err)

	dom, err := project.Domain("test_domain1", "")
	assert.NilError(t, err)

	err = dom.SetWithStruct(true, nil)
	assert.ErrorContains(t, err, "nil pointer")
}
