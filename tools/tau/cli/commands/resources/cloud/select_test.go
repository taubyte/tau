package cloud

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/taubyte/tau/tools/tau/common"
	"github.com/taubyte/tau/tools/tau/config"
	"github.com/taubyte/tau/tools/tau/session"
	"github.com/taubyte/tau/tools/tau/testutil"
	"github.com/urfave/cli/v2"
	"gotest.tools/v3/assert"
)

func TestSelectCloud_WithFQDN_NonInteractive(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "tau.yaml")

	configYaml := `
profiles:
  test:
    provider: github
    token: "123456"
    default: true
    git_username: test
    git_email: test@test.com
    type: Remote
    network: sandbox.taubyte.com
projects: {}
`
	err := os.WriteFile(configPath, []byte(configYaml), 0644)
	assert.NilError(t, err)

	session.Clear()
	config.Clear()
	t.Cleanup(func() {
		session.Clear()
		config.Clear()
	})

	t.Cleanup(testutil.WithConfigPath(configPath))

	var l link
	baseCmd, baseOps := l.Base()
	selCmd := l.Select()
	parentSelect := &cli.Command{Name: "select"}
	cloudCmd := selCmd.Initialize(parentSelect, baseCmd, baseOps)
	parentSelect.Subcommands = []*cli.Command{cloudCmd}

	app := &cli.App{
		Name:                   "tau",
		UseShortOptionHandling: true,
		Commands:               []*cli.Command{parentSelect},
	}

	err = app.Run([]string{"tau", "select", "cloud", "--fqdn", "sandbox.taubyte.com"})
	if err != nil {
		assert.Assert(t, err != nil)
		return
	}
	cloudType, ok := session.GetSelectedCloud()
	assert.Assert(t, ok, "selected cloud should be set")
	assert.Equal(t, cloudType, common.RemoteCloud)
}

func TestSelectCloud_Help(t *testing.T) {
	var l link
	baseCmd, baseOps := l.Base()
	selCmd := l.Select()
	parentSelect := &cli.Command{Name: "select"}
	cloudCmd := selCmd.Initialize(parentSelect, baseCmd, baseOps)
	parentSelect.Subcommands = []*cli.Command{cloudCmd}
	app := &cli.App{Name: "tau", Commands: []*cli.Command{parentSelect}}
	err := app.Run([]string{"tau", "select", "cloud", "--help"})
	assert.NilError(t, err)
}

func TestSelectCloud_BothFlagsSet_ReturnsFlagError(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "tau.yaml")
	configYaml := `
profiles:
  test:
    provider: github
    token: "123456"
    default: true
    type: Remote
projects: {}
`
	err := os.WriteFile(configPath, []byte(configYaml), 0644)
	assert.NilError(t, err)

	session.Clear()
	config.Clear()
	t.Cleanup(func() {
		session.Clear()
		config.Clear()
	})
	t.Cleanup(testutil.WithConfigPath(configPath))

	var l link
	baseCmd, baseOps := l.Base()
	selCmd := l.Select()
	parentSelect := &cli.Command{Name: "select"}
	cloudCmd := selCmd.Initialize(parentSelect, baseCmd, baseOps)
	parentSelect.Subcommands = []*cli.Command{cloudCmd}
	app := &cli.App{Name: "tau", UseShortOptionHandling: true, Commands: []*cli.Command{parentSelect}}

	err = app.Run([]string{"tau", "select", "cloud", "--fqdn", "sandbox.taubyte.com", "--universe", "some-universe"})
	assert.ErrorContains(t, err, "only set one flag")
}
