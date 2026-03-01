package codefile

import (
	"path/filepath"
	"strings"
	"testing"

	schemaCommon "github.com/taubyte/tau/pkg/schema/common"
	"github.com/taubyte/tau/tools/tau/session"
	"github.com/taubyte/tau/tools/tau/testutil"
	"gotest.tools/v3/assert"
)

func TestPath_TCCFixture_NoApp(t *testing.T) {
	testutil.WithTCCFixtureEnv(t)
	p, err := Path("myfunc", schemaCommon.FunctionFolder)
	assert.NilError(t, err)
	// Path = projectConfig.CodeLoc() + "/" + folder + "/" + name = .../fixtures/code/functions/myfunc
	assert.Assert(t, strings.HasSuffix(p.String(), filepath.Join("code", "functions", "myfunc")))
	assert.Assert(t, strings.Contains(p.String(), "fixtures"))
}

func TestPath_TCCFixture_WithApp(t *testing.T) {
	testutil.WithTCCFixtureEnv(t)
	session.Set().SelectedApplication("test_app1")
	p, err := Path("myfunc", schemaCommon.FunctionFolder)
	assert.NilError(t, err)
	// Path = CodeLoc() + "/applications/test_app1/functions/myfunc"
	assert.Assert(t, strings.Contains(p.String(), "applications"))
	assert.Assert(t, strings.Contains(p.String(), "test_app1"))
	assert.Assert(t, strings.HasSuffix(p.String(), filepath.Join("functions", "myfunc")))
}
