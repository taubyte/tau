package libraries_test

import (
	_ "embed"
	"testing"

	internal "github.com/taubyte/tau/pkg/schema/internal/test"
	"github.com/taubyte/tau/pkg/schema/libraries"
	"gotest.tools/v3/assert"
)

func TestYaml(t *testing.T) {
	getter, err := libraries.Yaml("test_library1", "", internal.Library1)
	assert.NilError(t, err)

	assertLibrary1(t, getter)

	getter, err = libraries.Yaml("test_library2", "test_app1", internal.Library2)
	assert.NilError(t, err)

	assertLibrary2(t, getter)
}
