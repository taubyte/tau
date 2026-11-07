package project_test

import (
	"net/http"
	"testing"

	"github.com/h2non/gock"
	"github.com/taubyte/tau/tools/tau/cli"
	"github.com/taubyte/tau/tools/tau/testutil"
	"gotest.tools/v3/assert"
)

// A dashed project name is refused, with the underscore form suggested, before
// anything is asked of auth (and through it GitHub).
func TestNewProjectDashedNameRefusedBeforeAuth(t *testing.T) {
	gock.DisableNetworking()
	defer gock.EnableNetworking()
	defer gock.Off()
	calls := 0
	gock.New(testutil.AuthMockBaseURL).
		AddMatcher(func(*http.Request, *gock.Request) (bool, error) { calls++; return true, nil }).
		Persist().
		Reply(500)
	gock.Intercept()

	dir, projectPath, cfg := resourceTestDir(t)
	_, _, err := testutil.RunCLIWithDirAndCwdWithAuthMock(t, cli.Run, dir, projectPath, cfg,
		"--defaults", "--yes", "--color", "never", "new", "project", "--name", "my-app", "--private")
	assert.ErrorContains(t, err, "use `my_app`")
	assert.Equal(t, calls, 0, "no auth call may happen before the name is refused")
}
