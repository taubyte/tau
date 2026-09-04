package emit_test

import (
	"context"
	"testing"

	"github.com/spf13/afero"
	"github.com/stretchr/testify/require"

	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

// compiles is the assertion every test here really makes: tcc reads what was
// written and produces an object.
//
// IT USES THE REAL COMPILER, not the session's own validate, because the two
// are different claims. Validate says the fields are individually acceptable;
// this says the project is a project.
func compiles(t *testing.T, fs afero.Fs, dir string) {
	t.Helper()

	c, err := schema.New(schema.WithVirtual(fs, dir), schema.WithBranch("main"))
	require.NoError(t, err)

	obj, _, err := c.Compile(context.Background())
	require.NoError(t, err)
	require.NotNil(t, obj)
}
