//go:build dreaming

package fixtures

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/core/services/patrick"
	"github.com/taubyte/tau/dream"
	commonTest "github.com/taubyte/tau/dream/helpers"
	projectLib "github.com/taubyte/tau/pkg/schema/project"
	specs "github.com/taubyte/tau/pkg/specs/methods"
	"github.com/taubyte/tau/utils"
	"gotest.tools/v3/assert"

	_ "github.com/taubyte/tau/services/auth/dream"
	_ "github.com/taubyte/tau/services/hoarder/dream"
	_ "github.com/taubyte/tau/services/monkey/dream"
	_ "github.com/taubyte/tau/services/patrick/dream"
	_ "github.com/taubyte/tau/services/tns/dream"

	_ "github.com/taubyte/tau/clients/p2p/auth/dream"
	_ "github.com/taubyte/tau/clients/p2p/patrick/dream"
	_ "github.com/taubyte/tau/clients/p2p/tns/dream"
)

// tccFixtureConfigPath returns the absolute path to the TCC fixture config dir (pkg/tcc/taubyte/v1/fixtures/config).
func tccFixtureConfigPath(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	assert.NilError(t, err)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(dir, "pkg", "tcc", "taubyte", "v1", "fixtures", "config")
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("repo root (go.mod) not found")
			return ""
		}
		dir = parent
	}
}

func TestLocalPushConfigCompile_Dreaming(t *testing.T) {
	m, err := dream.New(t.Context())
	assert.NilError(t, err)
	defer m.Close()

	u, err := m.New(dream.UniverseConfig{Name: t.Name()})
	assert.NilError(t, err)

	err = u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"auth":    {},
			"patrick": {},
			"tns":     {},
			"monkey":  {},
			"hoarder": {},
		},
		Simples: map[string]dream.SimpleConfig{
			"client": {
				Clients: dream.SimpleConfigClients{
					TNS:     &commonIface.ClientConfig{},
					Patrick: &commonIface.ClientConfig{},
					Auth:    &commonIface.ClientConfig{},
				}.Compat(),
			},
		},
	})
	assert.NilError(t, err)

	err = commonTest.CreateTestProject(u)
	assert.NilError(t, err)

	configDir := filepath.Join(t.TempDir(), "config")
	err = utils.CopyDir(tccFixtureConfigPath(t), configDir)
	assert.NilError(t, err)

	proj, err := projectLib.Open(projectLib.SystemFS(configDir))
	assert.NilError(t, err)
	err = proj.Set(true, projectLib.Id(commonTest.ProjectID))
	assert.NilError(t, err)

	repo, err := git.PlainInit(configDir, false)
	assert.NilError(t, err)
	w, err := repo.Worktree()
	assert.NilError(t, err)
	_, err = w.Add(".")
	assert.NilError(t, err)
	_, err = w.Commit("init", &git.CommitOptions{})
	assert.NilError(t, err)

	simple, err := u.Simple("client")
	assert.NilError(t, err)
	patrickClient, err := simple.Patrick()
	assert.NilError(t, err)

	err = u.RunFixture("pushConfig", configDir)
	assert.NilError(t, err)

	var newJobID string
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		jobs, err := patrickClient.List()
		assert.NilError(t, err)
		if len(jobs) > 0 {
			newJobID = jobs[0]
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	assert.Assert(t, newJobID != "", "expected a job to appear after pushConfig")

	timeout := 90 * time.Second
	pollInterval := 2 * time.Second
	deadline = time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		job, err := patrickClient.Get(newJobID)
		assert.NilError(t, err)
		switch job.Status {
		case patrick.JobStatusSuccess:
			// Optionally verify TNS has project config
			tnsClient, err := simple.TNS()
			assert.NilError(t, err)
			prefix := specs.ProjectPrefix(
				commonTest.ProjectID,
				job.Meta.Repository.Branch,
				job.Meta.HeadCommit.ID,
			)
			obj, err := tnsClient.Fetch(prefix)
			assert.NilError(t, err)
			assert.Assert(t, obj != nil && obj.Interface() != nil, "expected project config in TNS")
			return
		case patrick.JobStatusFailed, patrick.JobStatusCancelled:
			t.Fatalf("job %s ended with status %v", newJobID, job.Status)
		}
		time.Sleep(pollInterval)
	}
	t.Fatalf("job %s did not reach success within %v", newJobID, timeout)
}
