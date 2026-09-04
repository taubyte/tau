package importcmd

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v2"
)

// runImport drives the command the way the CLI does, so what is tested is the
// command and not a function beside it.
func runImport(t *testing.T, args ...string) (string, error) {
	t.Helper()

	var out bytes.Buffer
	app := &cli.App{
		Writer:         &out,
		Commands:       []*cli.Command{Command},
		ExitErrHandler: func(*cli.Context, error) {},
	}

	err := app.Run(append([]string{"tau", "import"}, args...))
	return out.String(), err
}

// write puts a source file in a fresh directory named after the project, which
// is where the project's name comes from.
func write(t *testing.T, dir, name, content string) string {
	t.Helper()

	path := filepath.Join(dir, name)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

const realistic = `
services:
  api:
    image: ghcr.io/acme/api:1.4
    command: ["./api"]
    ports:
      - "8080:8080"
    environment:
      LOG_LEVEL: info
    restart: unless-stopped
    deploy:
      resources:
        limits:
          memory: 512M
  db:
    image: postgres:16
    volumes:
      - pgdata:/var/lib/postgresql/data
volumes:
  pgdata: {}
`

// TestAComposeFileBecomesAProjectOnDisk — the whole command, end to end.
func TestAComposeFileBecomesAProjectOnDisk(t *testing.T) {
	root := t.TempDir()
	source := write(t, filepath.Join(root, "shop"), "docker-compose.yml", realistic)
	out := filepath.Join(root, "imported")

	printed, err := runImport(t, "--out", out, source)
	require.NoError(t, err, printed)

	for _, file := range []string{
		"project.yaml", "application/api.yaml", "application/db.yaml", "volume/pgdata.yaml",
	} {
		_, err := os.Stat(filepath.Join(out, file))
		require.NoError(t, err, file)
	}

	// THE PROJECT IS NAMED AFTER ITS DIRECTORY. Every compose file is called
	// docker-compose.yml, so naming the project after the file would call every
	// imported project the same thing.
	project, err := os.ReadFile(filepath.Join(out, "project.yaml"))
	require.NoError(t, err)
	require.Contains(t, string(project), "name: shop")

	// And the report says what did not carry, which is the product.
	require.Contains(t, printed, "read "+source+" as compose")
	require.Contains(t, printed, "blocked")
	require.Contains(t, printed, "NO_TAU_FEATURE:command")
	require.Contains(t, printed, "NO_TAU_FEATURE:restart")
	require.Contains(t, printed, "missing")
	require.Contains(t, printed, "volume/pgdata: quota")
	require.Contains(t, printed, "does not compile yet")
}

// TestADryRunWritesNothingAndStillReportsEverything. Reporting without emitting
// would have to duplicate what the emitter does to know what is missing, and
// the two copies would disagree.
func TestADryRunWritesNothing(t *testing.T) {
	root := t.TempDir()
	source := write(t, filepath.Join(root, "shop"), "docker-compose.yml", realistic)
	out := filepath.Join(root, "imported")

	printed, err := runImport(t, "--dry-run", "--out", out, source)
	require.NoError(t, err, printed)

	_, err = os.Stat(out)
	require.True(t, os.IsNotExist(err), "nothing was written")

	require.Contains(t, printed, "dry run: nothing was written")
	require.Contains(t, printed, "application/api.yaml", "and it still says what it would write")
	require.Contains(t, printed, "NO_TAU_FEATURE:command")
}

// TestTheNameCanBeGivenRatherThanInferred.
func TestTheNameCanBeGiven(t *testing.T) {
	root := t.TempDir()
	source := write(t, filepath.Join(root, "shop"), "docker-compose.yml", realistic)

	printed, err := runImport(t, "--name", "storefront", "--out", filepath.Join(root, "out"), source)
	require.NoError(t, err, printed)

	project, err := os.ReadFile(filepath.Join(root, "out", "project.yaml"))
	require.NoError(t, err)
	require.Contains(t, string(project), "name: storefront")
}

// TestADirectoryThatIsNotAV2NameIsStillAProjectName.
func TestADirectoryNameIsSanitised(t *testing.T) {
	root := t.TempDir()
	source := write(t, filepath.Join(root, "my-shop"), "docker-compose.yml", realistic)

	printed, err := runImport(t, "--out", filepath.Join(root, "out"), source)
	require.NoError(t, err, printed)

	project, err := os.ReadFile(filepath.Join(root, "out", "project.yaml"))
	require.NoError(t, err)
	require.Contains(t, string(project), "name: my_shop")
}

// TestAProjectWithNothingMissingSaysItCompiles, which is the other half of the
// report and the one nobody should have to guess at.
func TestAnImportThatCompilesSaysSo(t *testing.T) {
	root := t.TempDir()
	source := write(t, filepath.Join(root, "shop"), "docker-compose.yml", `
services:
  api:
    image: ghcr.io/acme/api:1.4
    deploy:
      resources:
        limits:
          memory: 512M
`)

	printed, err := runImport(t, "--out", filepath.Join(root, "out"), source)
	require.NoError(t, err, printed)
	require.Contains(t, printed, "it compiles.")
	require.NotContains(t, printed, "does not compile")
}

func TestWhatTheCommandRefuses(t *testing.T) {
	root := t.TempDir()

	_, err := runImport(t)
	require.Error(t, err, "one file, named")

	_, err = runImport(t, "a.yml", "b.yml")
	require.Error(t, err)

	_, err = runImport(t, filepath.Join(root, "nothing-here.yml"))
	require.Error(t, err)

	notes := write(t, root, "notes.txt", "hello\n")
	_, err = runImport(t, notes)
	require.ErrorIs(t, err, ErrUnrecognised)

	compose := write(t, filepath.Join(root, "shop"), "docker-compose.yml", realistic)
	_, err = runImport(t, "--from", "heroku", compose)
	require.Error(t, err)
}

// TestAFileAtTheRootStillGetsAName. A source file with no directory above it
// has nothing to be named after, and "imported" is better than empty.
func TestAFileWithNoDirectoryToBeNamedAfter(t *testing.T) {
	require.Equal(t, "imported", projectNameFor("/"))
	require.NotEmpty(t, projectNameFor("docker-compose.yml"))
}
