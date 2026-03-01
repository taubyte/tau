package projectLib_test

import (
	"testing"

	projectLib "github.com/taubyte/tau/tools/tau/lib/project"
	"github.com/taubyte/tau/tools/tau/session"
	"github.com/urfave/cli/v2"
	"gotest.tools/v3/assert"
)

func TestSelect_Deselect(t *testing.T) {
	session.Clear()
	defer session.Clear()

	dir := t.TempDir()
	assert.NilError(t, session.LoadSessionInDir(dir))

	ctx := &cli.Context{}

	err := projectLib.Select(ctx, "myproject")
	assert.NilError(t, err)
	name, ok := session.Get().SelectedProject()
	assert.Assert(t, ok)
	assert.Equal(t, name, "myproject")

	err = projectLib.Deselect(ctx, "ignored")
	assert.NilError(t, err)
	_, ok = session.Get().SelectedProject()
	assert.Assert(t, !ok)
}
