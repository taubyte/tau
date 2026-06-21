package website_test

import (
	"testing"

	internal "github.com/taubyte/tau/pkg/schema/internal/test"
	"github.com/taubyte/tau/pkg/schema/website"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"gotest.tools/v3/assert"
)

func TestSetBindings(t *testing.T) {
	project, closeFn, err := internal.NewProjectCopy()
	assert.NilError(t, err)
	defer closeFn()

	web, err := project.Website("test_website1", "")
	assert.NilError(t, err)

	bindings := []structureSpec.Binding{
		{Name: "CACHE", Type: structureSpec.BindingKV, Resource: "/cache"},
		{Name: "FILES", Type: structureSpec.BindingStorage, Resource: "/uploads"},
		{Name: "API_KEY", Type: structureSpec.BindingSecret, Resource: "MYAPP_API_KEY"},
	}

	err = web.Set(true, website.Bindings(bindings))
	assert.NilError(t, err)

	// Read back via the getter and via Struct() — both must round-trip.
	for _, got := range [][]structureSpec.Binding{
		web.Get().Bindings(),
		mustStruct(t, web).Bindings,
	} {
		assert.Equal(t, len(got), 3)
		byName := map[string]structureSpec.Binding{}
		for _, b := range got {
			byName[b.Name] = b
		}
		assert.Equal(t, byName["CACHE"].Type, structureSpec.BindingKV)
		assert.Equal(t, byName["CACHE"].Resource, "/cache")
		assert.Equal(t, byName["FILES"].Type, structureSpec.BindingStorage)
		assert.Equal(t, byName["API_KEY"].Type, structureSpec.BindingSecret)
		assert.Equal(t, byName["API_KEY"].Resource, "MYAPP_API_KEY")
	}
}

func mustStruct(t *testing.T, web website.Website) *structureSpec.Website {
	t.Helper()
	s, err := web.Get().Struct()
	assert.NilError(t, err)
	return s
}
