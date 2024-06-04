package domains_test

import (
	"fmt"
	"runtime"
	"testing"

	"github.com/taubyte/tau/pkg/schema/domains"
	internal "github.com/taubyte/tau/pkg/schema/internal/test"
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

func assertDomain1(t *testing.T, getter domains.Getter) {
	eql(t, [][]any{
		{getter.Id(), "domain1ID"},
		{getter.Name(), "test_domain1"},
		{getter.Description(), "a domain for hal computers"},
		{getter.Tags(), []string{"domain_tag_1", "domain_tag_2"}},
		{getter.FQDN(), "hal.computers.com"},
		{getter.UseCertificate(), true},
		{getter.Key(), "testKey"},
		{getter.Cert(), "testCert"},
		{getter.Application(), ""},
		{len(getter.SmartOps()), 0},
	})
}

func assertDomain2(t *testing.T, getter domains.Getter) {
	eql(t, [][]any{
		{getter.Id(), "domain2ID"},
		{getter.Name(), "test_domain2"},
		{getter.Description(), "a domain for app computers"},
		{getter.Tags(), []string{"domain_tag_3", "domain_tag_4"}},
		{getter.FQDN(), "app.computers.com"},
		{getter.UseCertificate(), false},
		{getter.Key(), ""},
		{getter.Cert(), ""},
		{getter.Application(), "test_app1"},
		{len(getter.SmartOps()), 0},
	})
}

func TestGet(t *testing.T) {
	project, err := internal.NewProjectReadOnly()
	assert.NilError(t, err)

	dom, err := project.Domain("test_domain1", "")
	assert.NilError(t, err)

	assertDomain1(t, dom.Get())

	dom, err = project.Domain("test_domain2", "test_app1")
	assert.NilError(t, err)

	assertDomain2(t, dom.Get())
}
