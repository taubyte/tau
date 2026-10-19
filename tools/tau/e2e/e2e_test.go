//go:build dreaming

// Package e2e runs the CLI against a real v2 fleet: a dream universe of two
// nodes, one running the build frontend (the public role) and the other every
// private service it reaches — auth, the build backend, secrets, register. The
// forge is pkg/forge/fake, so "write access" is a real answer from a real
// driver, just not one that needs the internet.
package e2e

import (
	"bytes"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/node"
	"github.com/taubyte/tau/node/dream"
	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/forge/fake"
	"github.com/taubyte/tau/tools/tau/internal/cli"
	"github.com/taubyte/tau/tools/tau/internal/config"
)

type result struct {
	code           int
	stdout, stderr string
}

func tau(args ...string) result {
	var out, errs bytes.Buffer
	code := cli.Run(cli.Env{Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errs, HTTP: http.DefaultClient}, args)
	return result{code, out.String(), errs.String()}
}

func ok(t *testing.T, args ...string) string {
	t.Helper()
	r := tau(args...)
	require.Equal(t, 0, r.code, "tau %s\nstdout: %s\nstderr: %s", strings.Join(args, " "), r.stdout, r.stderr)
	return r.stdout
}

func TestTheCLIAgainstARealFleet(t *testing.T) {
	t.Setenv(config.EnvConfig, filepath.Join(t.TempDir(), "config.yaml"))
	t.Setenv(config.EnvToken, "")
	t.Setenv(config.EnvCloud, "")

	f := fake.New(fake.GitHubLike)
	forges, err := f.Registry(forge.Policy{})
	require.NoError(t, err)

	u, err := dream.Start(t.Context(), t.Name(), dream.Config{
		Copies:  2,
		Options: []node.Option{node.WithForges(forges)},
		Configure: func(index int, c *node.Config) {
			if index == 1 {
				c.Services = []string{node.Discovery, node.BuildFrontend}
				c.BuildFrontend.Listen = "127.0.0.1:0"
			}
		},
	})
	require.NoError(t, err)
	t.Cleanup(u.Close)

	front, found := dream.Node(u.Pids()[1])
	require.True(t, found)
	addr := front.BuildFrontendAddr()
	require.NotNil(t, addr)
	cloud := "http://" + addr.String()

	ok(t, "login", "--cloud", cloud, "--token", f.TokenString())

	// A generated project compiles and audits clean, with no cloud involved.
	dir := t.TempDir()
	ok(t, "init", "shop", "--dir", dir)
	require.Contains(t, ok(t, "validate", "--dir", dir), "✓ compiles")

	// Linking reaches auth through the frontend's one public door to it. The
	// fleet has just booted, so the frontend may not have found auth yet.
	configRepo, codeRepo := f.Keys().Admin.String(), f.Keys().Writable.String()
	var link result
	require.Eventually(t, func() bool {
		link = tau("link", "--dir", dir, "--config-repo", configRepo, "--code-repo", codeRepo, "-o", "json")
		return link.code == 0
	}, 2*time.Minute, time.Second, "link never succeeded: %s", link.stderr)
	var linked config.Link
	require.NoError(t, json.Unmarshal([]byte(link.stdout), &linked))
	require.NotEmpty(t, linked.ProjectID)

	// A token that may not write to the repositories is refused, by the forge.
	r := tau("link", "--dir", t.TempDir(), "--name", "theirs", "--config-repo", f.Keys().NoAccess.String(), "--code-repo", codeRepo)
	require.NotZero(t, r.code)

	// Secrets: set, then listed by name only.
	require.Eventually(t, func() bool {
		return tau("secrets", "set", "DB_URL", "postgres://x", "--dir", dir, "--env", "prod").code == 0
	}, time.Minute, time.Second)
	var names []string
	require.NoError(t, json.Unmarshal([]byte(ok(t, "secrets", "list", "--dir", dir, "--env", "prod", "-o", "json")), &names))
	require.Equal(t, []string{"DB_URL"}, names)
	ok(t, "secrets", "unset", "DB_URL", "--dir", dir, "--env", "prod", "--yes")

	// Jobs: none yet, because nothing has been pushed.
	var jobs []any
	require.NoError(t, json.Unmarshal([]byte(ok(t, "jobs", "--dir", dir, "-o", "json")), &jobs))
	require.Empty(t, jobs)
	require.Contains(t, ok(t, "status", "--dir", dir), linked.ProjectID)

	// A token the forge does not know is refused as unauthenticated.
	t.Setenv(config.EnvToken, f.BadTokenString())
	r = tau("jobs", "--dir", dir)
	require.NotZero(t, r.code)
	require.Contains(t, r.stderr, "unauthorized")
}
