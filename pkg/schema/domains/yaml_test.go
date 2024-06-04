package domains_test

import (
	_ "embed"
	"testing"

	"github.com/taubyte/tau/pkg/schema/domains"
	internal "github.com/taubyte/tau/pkg/schema/internal/test"
	"gotest.tools/v3/assert"
)

func TestYaml(t *testing.T) {
	getter, err := domains.Yaml("test_domain1", "", internal.Domain1)
	assert.NilError(t, err)

	assertDomain1(t, getter)

	getter, err = domains.Yaml("test_domain2", "test_app1", internal.Domain2)
	assert.NilError(t, err)

	assertDomain2(t, getter)
}
