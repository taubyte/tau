package services_test

import (
	_ "embed"
	"testing"

	internal "github.com/taubyte/tau/pkg/schema/internal/test"
	"github.com/taubyte/tau/pkg/schema/services"
	"gotest.tools/v3/assert"
)

func TestYaml(t *testing.T) {
	getter, err := services.Yaml("test_service1", "", internal.Service1)
	assert.NilError(t, err)

	assertService1(t, getter)

	getter, err = services.Yaml("test_service2", "test_app1", internal.Service2)
	assert.NilError(t, err)

	assertService2(t, getter)
}
