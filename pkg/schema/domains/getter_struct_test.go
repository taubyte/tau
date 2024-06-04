package domains_test

import (
	"testing"

	internal "github.com/taubyte/tau/pkg/schema/internal/test"
	"gotest.tools/v3/assert"
)

func TestGetStruct(t *testing.T) {
	project, err := internal.NewProjectReadOnly()
	assert.NilError(t, err)

	db, err := project.Domain("test_domain1", "")
	assert.NilError(t, err)

	_struct, err := db.Get().Struct()
	assert.NilError(t, err)

	eql(t, [][]any{
		{_struct.Id, "domain1ID"},
		{_struct.Name, "test_domain1"},
		{_struct.Description, "a domain for hal computers"},
		{_struct.Tags, []string{"domain_tag_1", "domain_tag_2"}},
		{_struct.Fqdn, "hal.computers.com"},
		{_struct.KeyFile, "testKey"},
		{_struct.CertFile, "testCert"},
		{len(_struct.SmartOps), 0},
	})

	db, err = project.Domain("test_domain2", "test_app1")
	assert.NilError(t, err)

	_struct, err = db.Get().Struct()
	assert.NilError(t, err)

	eql(t, [][]any{
		{_struct.Id, "domain2ID"},
		{_struct.Name, "test_domain2"},
		{_struct.Description, "a domain for app computers"},
		{_struct.Tags, []string{"domain_tag_3", "domain_tag_4"}},
		{_struct.Fqdn, "app.computers.com"},
		{_struct.KeyFile, ""},
		{_struct.CertFile, ""},
		{len(_struct.SmartOps), 0},
	})
}
