package tests

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"time"

	"github.com/taubyte/tau/clients/p2p/patrick/mock"

	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/core/services/patrick"
	"github.com/taubyte/tau/dream"
	"github.com/taubyte/tau/p2p/peer"
	"gotest.tools/v3/assert"

	commonTest "github.com/taubyte/tau/dream/helpers"
	gitTest "github.com/taubyte/tau/dream/helpers/git"
	tccCompiler "github.com/taubyte/tau/pkg/tcc/taubyte/v1"
	protocolCommon "github.com/taubyte/tau/services/common"
	"github.com/taubyte/tau/services/monkey"
	tcc "github.com/taubyte/tau/utils/tcc"

	_ "github.com/taubyte/tau/clients/p2p/auth/dream"
	_ "github.com/taubyte/tau/clients/p2p/monkey/dream"
	_ "github.com/taubyte/tau/clients/p2p/tns/dream"
	_ "github.com/taubyte/tau/services/auth/dream"
	_ "github.com/taubyte/tau/services/hoarder/dream"
	_ "github.com/taubyte/tau/services/tns/dream"

	"testing"
)

var generatedDomainRegExp = regexp.MustCompile(`^[^.]+\.g\.tau\.link$`)

func TestConfigJob(t *testing.T) {
	protocolCommon.MockedPatrick = true
	monkey.NewPatrick = func(ctx context.Context, node peer.Node) (patrick.Client, error) {
		return &mock.Starfish{Jobs: make(map[string]*patrick.Job, 0)}, nil
	}

	m, err := dream.New(t.Context())
	assert.NilError(t, err)
	defer m.Close()

	u, err := m.New(dream.UniverseConfig{Name: t.Name()})
	assert.NilError(t, err)

	err = u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"monkey":  {},
			"hoarder": {},
			"tns":     {},
			"auth":    {},
		},
		Simples: map[string]dream.SimpleConfig{
			"client": {
				Clients: dream.SimpleConfigClients{
					TNS:    &commonIface.ClientConfig{},
					Monkey: &commonIface.ClientConfig{},
					Auth:   &commonIface.ClientConfig{},
				}.Compat(),
			},
		},
	})
	assert.NilError(t, err)

	// wait a couple seconds for services to start
	time.Sleep(time.Second * 2)

	simple, err := u.Simple("client")
	assert.NilError(t, err)

	tnsClient, err := simple.TNS()
	assert.NilError(t, err)

	monkeyClient, err := simple.Monkey()
	assert.NilError(t, err)

	// Override auth method so that projectID is not changed
	protocolCommon.GetNewProjectID = func(args ...interface{}) string {
		return commonTest.ProjectID
	}

	mockAuth, err := simple.Auth()
	assert.NilError(t, err)

	err = commonTest.RegisterTestProject(u.Context(), mockAuth)
	assert.NilError(t, err)

	// Use a temporary directory to avoid modifying any existing testGIT directories
	gitRoot, err := os.MkdirTemp("", "testGIT-*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(gitRoot) // Clean up after test
	gitRootConfig := gitRoot + "/config"
	os.MkdirAll(gitRootConfig, 0755)

	// clone repo
	err = gitTest.CloneToDir(u.Context(), gitRootConfig, commonTest.ConfigRepo)
	assert.NilError(t, err)

	fakJob := &patrick.Job{}
	fakJob.Logs = make(map[string]string)
	fakJob.AssetCid = make(map[string]string)
	fakJob.Meta.Repository.ID = commonTest.ConfigRepo.ID
	fakJob.Meta.Repository.SSHURL = fmt.Sprintf("git@github.com:%s/%s", commonTest.GitUser, commonTest.ConfigRepo.Name)
	fakJob.Meta.Repository.Provider = "github"
	fakJob.Meta.Repository.Branch = "main" // Updated to match repository default branch
	fakJob.Meta.HeadCommit.ID = "QmaskdjfziUJHJjYfhaysgYGYyA"
	fakJob.Id = "jobforjob_test"

	// Create TCC compiler
	compiler, err := tccCompiler.New(
		tccCompiler.WithLocal(gitRootConfig),
		tccCompiler.WithBranch(fakJob.Meta.Repository.Branch),
	)
	assert.NilError(t, err)

	// Compile
	obj, validations, err := compiler.Compile(context.Background())
	assert.NilError(t, err)

	// Extract project ID from validations
	projectID, err := tcc.ExtractProjectID(validations)
	assert.NilError(t, err)

	// Process DNS validations (dev mode)
	err = tcc.ProcessDNSValidations(
		validations,
		generatedDomainRegExp,
		true, // dev mode
		nil,  // no DV key needed in dev mode
	)
	assert.NilError(t, err)

	// Extract object and indexes from Flat()
	flat := obj.Flat()
	object, ok := flat["object"].(map[string]interface{})
	assert.Assert(t, ok, "object not found in flat result")

	indexes, ok := flat["indexes"].(map[string]interface{})
	assert.Assert(t, ok, "indexes not found in flat result")

	// Publish to TNS
	err = tcc.Publish(
		tnsClient,
		object,
		indexes,
		projectID,
		fakJob.Meta.Repository.Branch,
		fakJob.Meta.HeadCommit.ID,
	)
	assert.NilError(t, err)

	err = u.Monkey().Patrick().(*mock.Starfish).AddJob(t, u.Monkey().Node(), fakJob)
	assert.NilError(t, err)

	err = waitForTestStatus(monkeyClient, fakJob.Id, patrick.JobStatusLocked)
	assert.NilError(t, err)

}
