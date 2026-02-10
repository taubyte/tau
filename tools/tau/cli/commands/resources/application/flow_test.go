package application_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/taubyte/tau/tools/tau/cli"
	applicationPrompts "github.com/taubyte/tau/tools/tau/prompts/application"
	"github.com/taubyte/tau/tools/tau/testutil"
	"gotest.tools/v3/assert"
)

func TestApplicationFlow(t *testing.T) {
	t.Run("new_and_query", func(t *testing.T) {
		dir, projectPath, cfg := testutil.FlowTestDirNoAuth(t)
		stdout, _, err := testutil.RunCLIWithDirAndCwd(t, cli.Run, dir, projectPath, cfg,
			"new", "-y", "application",
			"--name", "someApp",
			"--description", "some app desc",
			"--tags", "some, other, tags",
			"--color", "never",
		)
		assert.NilError(t, err)
		assert.Assert(t, strings.Contains(stdout, "Created application:"))
		assert.Assert(t, strings.Contains(stdout, "someApp"))

		stdout, _, err = testutil.RunCLIWithDirAndCwd(t, cli.Run, dir, projectPath, cfg,
			"query", "application", "--name", "someApp",
		)
		assert.NilError(t, err)
		assert.Assert(t, strings.Contains(stdout, "someApp"))
		assert.Assert(t, strings.Contains(stdout, "some app desc"))
	})

	t.Run("new_without_y_fails", func(t *testing.T) {
		dir, projectPath, cfg := testutil.FlowTestDirNoAuth(t)
		var err error
		func() {
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("panic: %v", r)
				}
			}()
			_, _, err = testutil.RunCLIWithDirAndCwd(t, cli.Run, dir, projectPath, cfg,
				"new", "application",
				"--name", "someApp",
				"--description", "some app desc",
				"--tags", "some, other, tags",
			)
		}()
		assert.Assert(t, err != nil, "expected non-interactive new to fail without -y")
	})

	t.Run("edit_and_query", func(t *testing.T) {
		dir, projectPath, cfg := testutil.FlowTestDirNoAuth(t)
		_, _, err := testutil.RunCLIWithDirAndCwd(t, cli.Run, dir, projectPath, cfg,
			"new", "-y", "application",
			"--name", "someApp",
			"--description", "some app desc",
			"--tags", "some, other, tags",
			"--color", "never",
		)
		assert.NilError(t, err)

		_, _, err = testutil.RunCLIWithDirAndCwd(t, cli.Run, dir, projectPath, cfg,
			"edit", "-y", "application",
			"--name", "someApp",
			"--description", "some nedwdadda",
			"--tags", "some, wack, tags",
			"--color", "never",
		)
		assert.NilError(t, err)

		stdout, _, err := testutil.RunCLIWithDirAndCwd(t, cli.Run, dir, projectPath, cfg,
			"query", "application", "--name", "someApp",
		)
		assert.NilError(t, err)
		assert.Assert(t, strings.Contains(stdout, "some nedwdadda"))
		assert.Assert(t, strings.Contains(stdout, "wack"))
	})

	t.Run("delete_then_query_fails", func(t *testing.T) {
		dir, projectPath, cfg := testutil.FlowTestDirNoAuth(t)
		_, _, err := testutil.RunCLIWithDirAndCwd(t, cli.Run, dir, projectPath, cfg,
			"new", "-y", "application",
			"--name", "someApp",
			"--description", "some app desc",
			"--tags", "some, other, tags",
			"--color", "never",
		)
		assert.NilError(t, err)

		_, _, err = testutil.RunCLIWithDirAndCwd(t, cli.Run, dir, projectPath, cfg,
			"delete", "-y", "application", "--name", "someApp", "--color", "never",
		)
		assert.NilError(t, err)

		_, stderr, err := testutil.RunCLIWithDirAndCwd(t, cli.Run, dir, projectPath, cfg,
			"query", "application", "--name", "someApp",
		)
		assert.Assert(t, err != nil)
		assert.Assert(t, strings.Contains(stderr, fmt.Sprintf(applicationPrompts.NotFound, "someApp")))
	})

	t.Run("select_from_new", func(t *testing.T) {
		dir, projectPath, cfg := testutil.FlowTestDirNoAuth(t)
		stdout, _, err := testutil.RunCLIWithDirAndCwd(t, cli.Run, dir, projectPath, cfg,
			"new", "-y", "application",
			"--name", "someApp",
			"--description", "some app desc",
			"--tags", "some, other, tags",
			"--color", "never",
		)
		assert.NilError(t, err)
		assert.Assert(t, strings.Contains(stdout, "Selected application:"))
		assert.Assert(t, strings.Contains(stdout, "someApp"))
	})

	t.Run("select_from_created", func(t *testing.T) {
		dir, projectPath, cfg := testutil.FlowTestDirNoAuth(t)
		_, _, err := testutil.RunCLIWithDirAndCwd(t, cli.Run, dir, projectPath, cfg,
			"new", "-y", "application", "--name", "someapp1", "--description", "d", "--tags", "t", "--color", "never",
		)
		assert.NilError(t, err)
		_, _, err = testutil.RunCLIWithDirAndCwd(t, cli.Run, dir, projectPath, cfg,
			"new", "-y", "application", "--name", "someapp2", "--description", "d", "--tags", "t", "--color", "never",
		)
		assert.NilError(t, err)

		stdout, _, err := testutil.RunCLIWithDirAndCwd(t, cli.Run, dir, projectPath, cfg,
			"select", "application", "--name", "someapp1", "--color", "never",
		)
		assert.NilError(t, err)
		assert.Assert(t, strings.Contains(stdout, "Selected application: someapp1"))
	})

	t.Run("select_nonexistent_fails", func(t *testing.T) {
		dir, projectPath, cfg := testutil.FlowTestDirNoAuth(t)
		_, _, err := testutil.RunCLIWithDirAndCwd(t, cli.Run, dir, projectPath, cfg,
			"new", "-y", "application", "--name", "someapp1", "--description", "d", "--tags", "t", "--color", "never",
		)
		assert.NilError(t, err)

		_, stderr, err := testutil.RunCLIWithDirAndCwd(t, cli.Run, dir, projectPath, cfg,
			"select", "application", "--name", "somenoneapp1",
		)
		assert.Assert(t, err != nil)
		assert.Assert(t, strings.Contains(stderr, "application `somenoneapp1` not found"))
	})

	t.Run("query_list", func(t *testing.T) {
		dir, projectPath, cfg := testutil.FlowTestDirNoAuth(t)
		for _, name := range []string{"someapp1", "someapp2", "someapp3", "someapp4", "someapp5"} {
			_, _, err := testutil.RunCLIWithDirAndCwd(t, cli.Run, dir, projectPath, cfg,
				"new", "-y", "application", "--name", name, "--description", "d", "--tags", "t", "--color", "never",
			)
			assert.NilError(t, err)
		}
		_, _, err := testutil.RunCLIWithDirAndCwd(t, cli.Run, dir, projectPath, cfg,
			"new", "-y", "application", "--name", "someapp13", "--description", "d", "--tags", "t", "--color", "never",
		)
		assert.NilError(t, err)
		_, _, err = testutil.RunCLIWithDirAndCwd(t, cli.Run, dir, projectPath, cfg,
			"delete", "-y", "application", "--name", "someapp13", "--color", "never",
		)
		assert.NilError(t, err)

		stdout, _, err := testutil.RunCLIWithDirAndCwd(t, cli.Run, dir, projectPath, cfg,
			"query", "application", "--list",
		)
		assert.NilError(t, err)
		for _, name := range []string{"someapp1", "someapp2", "someapp3", "someapp4", "someapp5"} {
			assert.Assert(t, strings.Contains(stdout, name))
		}
		assert.Assert(t, !strings.Contains(stdout, "someapp13"))
	})
}
