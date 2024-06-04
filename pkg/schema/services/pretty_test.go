package services_test

import (
	"testing"

	internal "github.com/taubyte/tau/pkg/schema/internal/test"
	"gotest.tools/v3/assert"
)

func TestPretty(t *testing.T) {
	project, err := internal.NewProjectReadOnly()
	assert.NilError(t, err)

	srv, err := project.Service("test_service1", "")
	assert.NilError(t, err)

	assert.DeepEqual(t, srv.Prettify(nil), map[string]interface{}{
		"Id":          "service1ID",
		"Name":        "test_service1",
		"Description": "a super simple protocol",
		"Tags":        []string{"service_tag_1", "service_tag_2"},
		"Protocol":    "/simple/v1",
	})
}
