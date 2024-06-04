package project_test

import (
	"testing"

	internal "github.com/taubyte/tau/pkg/schema/internal/test"
	"github.com/taubyte/tau/pkg/schema/project"
	"gotest.tools/v3/assert"
)

func TestSetBasic(t *testing.T) {
	p, err := internal.NewProjectEmpty()
	assert.NilError(t, err)

	p.Set(true,
		project.Id("testID"),
		project.Description("a different project"),
		project.Email("test@taubyte.com"),
	)

	eql(t, [][]any{
		{p.Get().Id(), "testID"},
		{p.Get().Description(), "a different project"},
		{p.Get().Email(), "test@taubyte.com"},
	})
}
