package domains_test

import (
	"testing"

	internal "github.com/taubyte/tau/pkg/schema/internal/test"
	"gotest.tools/v3/assert"
)

func TestPretty(t *testing.T) {
	project, err := internal.NewProjectReadOnly()
	assert.NilError(t, err)

	dom, err := project.Domain("test_domain1", "")
	assert.NilError(t, err)

	assert.DeepEqual(t, dom.Prettify(nil), map[string]interface{}{
		"Description":    "a domain for hal computers",
		"FQDN":           "hal.computers.com",
		"Id":             "domain1ID",
		"Name":           "test_domain1",
		"Tags":           []string{"domain_tag_1", "domain_tag_2"},
		"Type":           "inline",
		"UseCertificate": true,
	})
}
