//go:build dreaming && wasmtime_component

package fixtures

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/core/services/patrick"
	"github.com/taubyte/tau/dream"
	commonTest "github.com/taubyte/tau/dream/helpers"
	"github.com/taubyte/tau/pkg/specs/builders/frameworks"
	spec "github.com/taubyte/tau/pkg/specs/common"
	"github.com/taubyte/tau/pkg/specs/methods"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	websiteSpec "github.com/taubyte/tau/pkg/specs/website"
	"github.com/taubyte/tau/services/substrate/components/http/website/wasmtimehttp"
	"github.com/taubyte/tau/utils"
	tcc "github.com/taubyte/tau/utils/tcc"
	"gotest.tools/v3/assert"

	_ "github.com/taubyte/tau/clients/p2p/auth/dream"
	_ "github.com/taubyte/tau/clients/p2p/patrick/dream"
	_ "github.com/taubyte/tau/clients/p2p/tns/dream"
	_ "github.com/taubyte/tau/services/auth/dream"
	_ "github.com/taubyte/tau/services/hoarder/dream"
	_ "github.com/taubyte/tau/services/monkey/dream"
	_ "github.com/taubyte/tau/services/patrick/dream"
	_ "github.com/taubyte/tau/services/substrate/dream"
	_ "github.com/taubyte/tau/services/tns/dream"
)

const expressE2EMarker = "Express on Taubyte — push/build/serve e2e"

// e2eWebsiteId is this test's website resource id (any CID-shaped string).
const e2eWebsiteId = "QmcrzjxwbqERscawQcXW4e5jyNBNoxLsUYatn63E8XPQq2"

// TestLocalPushWebsiteSSRExpress_Dreaming is the full zero-config SSR deploy
// path, end to end, with NO pre-built artifacts: a plain Express repo (no
// `.taubyte`) is pushed into a real dream universe, monkey clones it, detects
// Express, generates the build config (services/monkey/jobs/framework.go),
// runs the build in the SSR builder image (npm ci → taubyte-ssr-adapter →
// wasi:http component), and the substrate serves the result through the
// component runtime. This is what `compileFor`-based tests shortcut.
//
// Prerequisites (skipped, with instructions, when missing):
//   - a reachable Docker daemon (monkey runs the build in a container; the
//     container itself needs outbound network for `npm ci`)
//   - the SSR builder image (see tools/ssr-builder/README.md):
//     docker build -t taubyte-ssr-builder:local -f tools/ssr-builder/Dockerfile .
//   - `wasmtime` on PATH (the substrate's component backend)
//
// Run:
//
//	go test -tags "dreaming wasmtime_component" -run TestLocalPushWebsiteSSRExpress_Dreaming -v -timeout 30m ./dream/fixtures/
func TestLocalPushWebsiteSSRExpress_Dreaming(t *testing.T) {
	requireSSRBuildToolchain(t)
	t.Cleanup(wasmtimehttp.ShutdownAll)

	m, err := dream.New(t.Context())
	assert.NilError(t, err)
	defer m.Close()

	u, err := m.New(dream.UniverseConfig{Name: t.Name()})
	assert.NilError(t, err)

	err = u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"auth":      {},
			"patrick":   {},
			"tns":       {},
			"monkey":    {},
			"hoarder":   {},
			"substrate": {},
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

	assert.NilError(t, commonTest.CreateTestProject(u))

	// The project config in TNS: a website resource bound to the test website
	// repo id — the push below arrives as that repo, which is how monkey maps
	// the job's build output onto this resource — served under the test fqdn.
	fs, _, err := tcc.GenerateProject(commonTest.ProjectID,
		&structureSpec.Website{
			Id: e2eWebsiteId, Name: "expressE2E", Domains: []string{"e2eDomain"},
			Paths: []string{"/"}, Provider: "github",
			RepoID:   strconv.Itoa(commonTest.WebsiteRepo.ID),
			RepoName: commonTest.GitUser + "/" + commonTest.WebsiteRepo.Name,
			Render:   websiteSpec.RenderSSR,
		},
		&structureSpec.Domain{Name: "e2eDomain", Fqdn: commonTest.TestFQDN},
	)
	assert.NilError(t, err)
	assert.NilError(t, u.RunFixture("injectProject", fs))

	// A bare Express app — package.json + lockfile + index.js, deliberately NO
	// `.taubyte` — committed to a local repo the push fixture hands to monkey
	// as a `local://` clone URL.
	repoDir := filepath.Join(t.TempDir(), "express-app")
	assert.NilError(t, utils.CopyDir(expressFixturePath(t), repoDir))

	repo, err := git.PlainInit(repoDir, false)
	assert.NilError(t, err)
	w, err := repo.Worktree()
	assert.NilError(t, err)
	_, err = w.Add(".")
	assert.NilError(t, err)
	sig := &object.Signature{Name: "test", Email: "test@test.com", When: time.Now()}
	_, err = w.Commit("init", &git.CommitOptions{Author: sig, Committer: sig})
	assert.NilError(t, err)

	simple, err := u.Simple("client")
	assert.NilError(t, err)
	patrickClient, err := simple.Patrick()
	assert.NilError(t, err)

	assert.NilError(t, u.RunFixture("pushWebsite", repoDir))

	// The push registers a patrick job; monkey picks it up and runs the real
	// build (clone → detect → generate → container). Wait it out.
	var jobID string
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		jobs, err := patrickClient.List()
		assert.NilError(t, err)
		if len(jobs) > 0 {
			jobID = jobs[0]
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	assert.Assert(t, jobID != "", "expected a job to appear after pushWebsite")

	// npm ci + esbuild + jco componentize, cold, inside the build container —
	// minutes, not seconds.
	timeout := 15 * time.Minute
	pollInterval := 5 * time.Second
	deadline = time.Now().Add(timeout)
	lastStatus := patrick.JobStatus(-1)
	for {
		if !time.Now().Before(deadline) {
			t.Fatalf("job %s did not finish within %v (last status %v)", jobID, timeout, lastStatus)
		}
		job, err := patrickClient.Get(jobID)
		assert.NilError(t, err)
		if job.Status != lastStatus {
			t.Logf("job %s status: %v", jobID, job.Status)
			lastStatus = job.Status
		}
		if job.Status == patrick.JobStatusSuccess {
			break
		}
		if job.Status == patrick.JobStatusFailed || job.Status == patrick.JobStatusCancelled {
			t.Fatalf("build job %s ended with status %v — check the monkey build output above (docker build container logs); is the SSR builder image present and does the container have network for `npm ci`?", jobID, job.Status)
		}
		time.Sleep(pollInterval)
	}

	// The website is now published: wait for the TNS index, then assert the
	// server-rendered response comes back through the substrate.
	assert.NilError(t, waitForWebsiteInTNSLocal(u, commonTest.TestFQDN, 60, time.Second))

	body, status, err := getHalRetry(u, "/", 60, time.Second)
	assert.NilError(t, err)
	if !strings.Contains(string(body), expressE2EMarker) {
		t.Fatalf("status %d, served response missing %q, got: %.300s", status, expressE2EMarker, body)
	}
	t.Logf("Express served from a real push→build→serve run (status %d) — contains %q", status, expressE2EMarker)
}

// requireSSRBuildToolchain skips the test unless docker (daemon reachable), the
// SSR builder image, and wasmtime are all available.
func requireSSRBuildToolchain(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH (the substrate component backend shells out to `wasmtime serve`)")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not on PATH (monkey runs website builds in containers)")
	}
	if err := exec.Command("docker", "version", "--format", "{{.Server.Version}}").Run(); err != nil {
		t.Skip("docker daemon not reachable (monkey runs website builds in containers)")
	}
	image := os.Getenv("TAUBYTE_SSR_BUILDER_IMAGE")
	if image == "" {
		image = frameworks.DefaultSSRBuilderImage
	}
	if err := exec.Command("docker", "image", "inspect", image).Run(); err != nil {
		t.Skipf("SSR builder image %q not present — build it first: docker build -t %s -f tools/ssr-builder/Dockerfile .", image, frameworks.DefaultSSRBuilderImage)
	}
}

// expressFixturePath returns the vendored zero-config Express app (testdata/express-ssr).
func expressFixturePath(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	assert.NilError(t, err)
	return filepath.Join(wd, "testdata", "express-ssr")
}

// waitForWebsiteInTNSLocal waits until the substrate's TNS view indexes a
// website under fqdn (same mechanics as the compile fixtures' helper).
func waitForWebsiteInTNSLocal(u *dream.Universe, fqdn string, maxRetries int, retryDelay time.Duration) error {
	substrate := u.Substrate()
	if substrate == nil {
		return fmt.Errorf("substrate service not available")
	}
	tns := substrate.Tns()
	if tns == nil {
		return fmt.Errorf("TNS client not available from substrate service")
	}

	httpPath, err := methods.HttpPath(fqdn, websiteSpec.PathVariable)
	if err != nil {
		return fmt.Errorf("creating HTTP path failed: %w", err)
	}
	linksPath := httpPath.Versioning().Links()

	var lastErr error
	for i := 0; i < maxRetries; i++ {
		indexObject, err := tns.Fetch(linksPath)
		if err == nil {
			pathList, err := indexObject.Current(spec.DefaultBranches)
			if err == nil && len(pathList) > 0 {
				return nil
			}
			lastErr = err
		} else {
			lastErr = err
		}
		time.Sleep(retryDelay)
	}
	return fmt.Errorf("website for %s not indexed in TNS after %d retries, last error: %v", fqdn, maxRetries, lastErr)
}

// getHalRetry GETs path on the test fqdn through the substrate node, retrying
// while the serviceable lookup has not converged yet.
func getHalRetry(u *dream.Universe, path string, maxRetries int, retryDelay time.Duration) ([]byte, int, error) {
	var lastErr error
	for i := 0; i < maxRetries; i++ {
		nodePort, err := u.GetPortHttp(u.Substrate().Node())
		if err != nil {
			return nil, 0, err
		}
		url := fmt.Sprintf("http://%s:%d%s", commonTest.TestFQDN, nodePort, path)
		var resp *http.Response
		resp, lastErr = commonTest.CreateHttpClient().Get(url)
		if lastErr == nil {
			defer resp.Body.Close()
			b, err := io.ReadAll(resp.Body)
			return b, resp.StatusCode, err
		}
		if !isServiceableLookupError(lastErr) {
			return nil, 0, lastErr
		}
		time.Sleep(retryDelay)
	}
	return nil, 0, fmt.Errorf("failed after %d retries, last error: %w", maxRetries, lastErr)
}

func isServiceableLookupError(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "http serviceable lookup failed") ||
		strings.Contains(s, "no HTTP match found") ||
		strings.Contains(s, "looking up serviceable failed")
}
