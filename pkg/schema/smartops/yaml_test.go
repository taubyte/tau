package smartops_test

import (
	_ "embed"
	"testing"

	internal "github.com/taubyte/tau/pkg/schema/internal/test"
	"github.com/taubyte/tau/pkg/schema/smartops"
	"gotest.tools/v3/assert"
)

func TestYaml(t *testing.T) {
	getter, err := smartops.Yaml("test_smartops1", "", internal.SmartOp1)
	assert.NilError(t, err)

	assertSmartops1(t, getter)

	getter, err = smartops.Yaml("test_smartops2", "test_app1", internal.SmartOp2)
	assert.NilError(t, err)

	assertSmartops2(t, getter)
}
