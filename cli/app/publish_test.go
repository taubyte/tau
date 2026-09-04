package app

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gotest.tools/v3/assert"

	"github.com/taubyte/tau/services/v2/node"
	"github.com/taubyte/tau/services/v2/node/boot"
)

// basicProject is the vocabulary's own fixture, which is a config repository
// like any other and the only one guaranteed to keep compiling. envsProject
// declares three deployments and a shadow, and suffixes keys for them.
const (
	basicProject = "../../pkg/tcc/taubyte/v2/fixtures/basic"
	envsProject  = "../../pkg/tcc/taubyte/v2/fixtures/environments"
)

// A DRY RUN NEEDS NO FLEET. Whether a config compiles is a question about the
// config, and answering it should not require a swarm key, a bootstrap peer, or
// anything else being up.
func TestPublishDryRun(t *testing.T) {
	out, err := run(t, "publish", "--dry-run", "-c", nodeFile(t, nil), "-p", "shop", "-e", "prod", basicProject)
	assert.NilError(t, err)
	assert.Assert(t, strings.Contains(out, "compiles"), out)

	// It says what the project would answer on, and separately what it cannot
	// name yet — a generated host is only half a name until the cloud says
	// which zone it is in.
	assert.Assert(t, strings.Contains(out, "not published"), out)
	assert.Assert(t, strings.Contains(out, "<cloud fqdn>"), out)
}

// With the zone, the generated names are whole and nothing is held back.
func TestPublishDryRunWithTheZone(t *testing.T) {
	out, err := run(t, "publish", "--dry-run", "--cloud-fqdn", "acme.example",
		"-c", nodeFile(t, nil), "-p", "shop", basicProject)
	assert.NilError(t, err)
	assert.Assert(t, strings.Contains(out, "acme.example"), out)
	assert.Assert(t, !strings.Contains(out, "not published"), out)
}

// THE WHOLE PATH: a fleet with a register, a directory on disk, and one command
// that compiles it, finds the register, and leaves something a worker can read.
// Nobody types a revision and nobody looks up which node holds what.
func TestPublishReachesAFleet(t *testing.T) {
	fleet := fleetWithARegister(t)

	out, err := run(t, "publish", "-c", nodeFile(t, fleet),
		"-p", "shop", "-e", "prod", "--cloud-fqdn", "acme.example", basicProject)
	assert.NilError(t, err)
	assert.Assert(t, strings.Contains(out, "published shop/prod revision 1"), out)
	assert.Assert(t, strings.Contains(out, "answers on"), out)

	// Publishing the same directory again does not move the pointer: the
	// content is the name, so a re-run is a no-op rather than a revision.
	out, err = run(t, "publish", "-c", nodeFile(t, fleet),
		"-p", "shop", "-e", "prod", "--cloud-fqdn", "acme.example", basicProject)
	assert.NilError(t, err)
	assert.Assert(t, strings.Contains(out, "unchanged"), out)
	assert.Assert(t, strings.Contains(out, "revision 1"), out)
}

// A PUSH IS THE DEPLOY. The project says which branches feed which of its
// deployments, so one push to `main` puts production and the shadow written
// inside it live — and nobody types an environment on a command line.
func TestPublishDeploysEveryEnvironmentTheBranchFeeds(t *testing.T) {
	fleet := fleetWithARegister(t)

	out, err := run(t, "publish", "-c", nodeFile(t, fleet),
		"-p", "shop", "--branch", "main", "--cloud-fqdn", "acme.example", envsProject)
	assert.NilError(t, err)

	for _, deployment := range []string{"shop/production", "shop/ab", "shop/preview"} {
		assert.Assert(t, strings.Contains(out, "published "+deployment+" revision 1"), out)
	}
	assert.Assert(t, !strings.Contains(out, "shop/staging"), out)

	// A different branch is a different set, and its revisions start at one of
	// their own: a deployment's revision is its own pointer's.
	out, err = run(t, "publish", "-c", nodeFile(t, fleet),
		"-p", "shop", "--branch", "release/1.4", "--cloud-fqdn", "acme.example", envsProject)
	assert.NilError(t, err)
	assert.Assert(t, strings.Contains(out, "published shop/staging revision 1"), out)

	// PREVIEW TAKES BOTH BRANCHES, and the branch is part of the compile — so
	// the second push publishes a different object over the first rather than
	// finding it unchanged. `branches: ["*"]` is ONE deployment fed by every
	// branch, and the last push wins it; a preview per branch would be a
	// deployment per branch, which the table can say and this project does not.
	assert.Assert(t, strings.Contains(out, "published shop/preview revision 2"), out)
}

// EACH DEPLOYMENT IS ITS OWN COMPILE, which is what makes a per-environment
// limit mean anything. The dry run says so per deployment rather than once.
func TestPublishCompilesEachDeploymentSeparately(t *testing.T) {
	out, err := run(t, "publish", "--dry-run", "-c", nodeFile(t, nil),
		"-p", "shop", "--branch", "main", envsProject)
	assert.NilError(t, err)

	for _, deployment := range []string{"production", "ab", "preview"} {
		assert.Assert(t, strings.Contains(out, "compiles for "+deployment), out)
	}
}

func TestPublishRefusesWhatItCannotDo(t *testing.T) {
	config := nodeFile(t, nil)

	_, err := run(t, "publish", "-c", config, "-p", "shop")
	assert.ErrorContains(t, err, "one config directory")

	_, err = run(t, "publish", "-c", config, "-p", "shop", "--dry-run", filepath.Join(t.TempDir(), "empty"))
	assert.Assert(t, err != nil)

	// A project name is not derivable from anything, and guessing one would
	// publish under a name nobody asked for.
	_, err = run(t, "publish", "-c", config, basicProject)
	assert.ErrorContains(t, err, "project")

	// A project whose deployments are fed by branches needs one of the two
	// things that can name a deployment.
	_, err = run(t, "publish", "--dry-run", "-c", config, "-p", "shop", envsProject)
	assert.ErrorContains(t, err, "branch")

	// A misspelled environment is not a new one.
	_, err = run(t, "publish", "--dry-run", "-c", config, "-p", "shop", "-e", "prodution", envsProject)
	assert.ErrorContains(t, err, "production")

	// Nothing runs a register, so the wait ends and says what it waited for.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	app := newApp()
	err = app.RunContext(ctx, []string{os.Args[0], "publish", "-c", config,
		"-p", "shop", "--wait", "600ms", basicProject})
	assert.ErrorContains(t, err, node.Register)
}

// fleetWithARegister starts one, and hands back the addresses to dial it on.
func fleetWithARegister(t *testing.T) []string {
	t.Helper()

	running, err := boot.Start(t.Context(), boot.File{
		Node:     node.Config{Services: []string{node.Register, node.Stats}, Cloud: "acme"},
		Root:     t.TempDir(),
		Listen:   []string{"/ip4/127.0.0.1/tcp/0"},
		SwarmKey: base64.StdEncoding.EncodeToString(swarmKey()),
	})
	assert.NilError(t, err)
	t.Cleanup(func() { running.Close() })

	var out []string
	for _, a := range running.Host().Addrs() {
		out = append(out, a.String()+"/p2p/"+running.Host().ID().String())
	}
	return out
}

// nodeFile writes the config the command joins the swarm with.
func nodeFile(t *testing.T, bootstrap []string) string {
	t.Helper()

	config := "" +
		"node:\n  cloud: acme\n" +
		"listen: [\"/ip4/127.0.0.1/tcp/0\"]\n" +
		"swarmkey: " + base64.StdEncoding.EncodeToString(swarmKey()) + "\n" +
		"bootstrap:\n"
	for _, address := range bootstrap {
		config += "  - " + address + "\n"
	}

	path := filepath.Join(t.TempDir(), "node.yaml")
	assert.NilError(t, os.WriteFile(path, []byte(config), 0o644))
	return path
}
