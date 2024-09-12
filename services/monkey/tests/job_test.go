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
	"github.com/taubyte/tau/pkg/config-compiler/compile"
	"gotest.tools/v3/assert"

	commonTest "github.com/taubyte/tau/dream/helpers"
	gitTest "github.com/taubyte/tau/dream/helpers/git"
	projectLib "github.com/taubyte/tau/pkg/schema/project"
	protocolCommon "github.com/taubyte/tau/services/common"
	"github.com/taubyte/tau/services/monkey"

	_ "github.com/taubyte/tau/clients/p2p/monkey"
	_ "github.com/taubyte/tau/clients/p2p/tns"
	_ "github.com/taubyte/tau/services/auth"
	_ "github.com/taubyte/tau/services/hoarder"
	_ "github.com/taubyte/tau/services/tns"

	"testing"
)

var generatedDomainRegExp = regexp.MustCompile(`^[^.]+\.g\.tau\.link$`)

func TestConfigJob(t *testing.T) {
	t.Skip("needs to be redone")
	protocolCommon.MockedPatrick = true
	monkey.NewPatrick = func(ctx context.Context, node peer.Node) (patrick.Client, error) {
		return &mock.Starfish{Jobs: make(map[string]*patrick.Job, 0)}, nil
	}

	u := dream.New(dream.UniverseConfig{Name: t.Name()})
	defer u.Stop()

	err := u.StartWithConfig(&dream.Config{
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
				}.Compat(),
			},
		},
	})
	if err != nil {
		t.Error(err)
		return
	}

	// wait a couple seconds for services to start
	time.Sleep(time.Second * 2)

	simple, err := u.Simple("client")
	if err != nil {
		t.Error(err)
		return
	}

	tnsClient, err := simple.TNS()
	assert.NilError(t, err)

	monkeyClient, err := simple.Monkey()
	assert.NilError(t, err)

	// Override auth method so that projectID is not changed
	protocolCommon.GetNewProjectID = func(args ...interface{}) string {
		return commonTest.ProjectID
	}

	authHttpURL, err := u.GetURLHttp(u.Auth().Node())
	if err != nil {
		t.Error(err)
		return
	}

	err = commonTest.RegisterTestProject(u.Context(), authHttpURL)
	if err != nil {
		t.Error(err)
		return
	}

	gitRoot := "./testGIT"
	gitRootConfig := gitRoot + "/config"
	os.MkdirAll(gitRootConfig, 0755)
	defer os.RemoveAll(gitRootConfig)

	// clone repo
	err = gitTest.CloneToDirSSH(u.Context(), gitRootConfig, commonTest.ConfigRepo)
	if err != nil {
		t.Error(err)
		return
	}

	// read with seer
	projectIface, err := projectLib.Open(projectLib.SystemFS(gitRootConfig))
	if err != nil {
		t.Error(err)
		return
	}

	fakJob := &patrick.Job{}
	fakJob.Logs = make(map[string]string)
	fakJob.AssetCid = make(map[string]string)
	fakJob.Meta.Repository.ID = commonTest.ConfigRepo.ID
	fakJob.Meta.Repository.SSHURL = fmt.Sprintf("git@github.com:%s/%s", commonTest.GitUser, commonTest.ConfigRepo.Name)
	fakJob.Meta.Repository.Provider = "github"
	fakJob.Meta.Repository.Branch = "master"
	fakJob.Meta.HeadCommit.ID = "QmaskdjfziUJHJjYfhaysgYGYyA"
	fakJob.Id = "jobforjob_test"
	rc, err := compile.CompilerConfig(projectIface, fakJob.Meta, generatedDomainRegExp)
	if err != nil {
		t.Error(err)
		return
	}

	compiler, err := compile.New(rc, compile.Dev())
	if err != nil {
		t.Error(err)
		return
	}

	defer compiler.Close()
	err = compiler.Build()
	if err != nil {
		t.Error(err)
		return
	}

	err = compiler.Publish(tnsClient)
	if err != nil {
		t.Error(err)
		return
	}

	err = u.Monkey().Patrick().(*mock.Starfish).AddJob(t, u.Monkey().Node(), fakJob)
	if err != nil {
		t.Error(err)
		return
	}

	err = waitForTestStatus(monkeyClient, fakJob.Id, patrick.JobStatusSuccess)
	if err != nil {
		t.Error(err)
		return
	}

}
