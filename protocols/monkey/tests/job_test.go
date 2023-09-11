package tests

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/taubyte/config-compiler/compile"
	commonIface "github.com/taubyte/go-interfaces/common"
	"github.com/taubyte/go-interfaces/services/patrick"
	"github.com/taubyte/p2p/peer"
	dreamland "github.com/taubyte/tau/libdream"
	"gotest.tools/v3/assert"

	projectLib "github.com/taubyte/go-project-schema/project"
	commonTest "github.com/taubyte/tau/libdream/helpers"
	gitTest "github.com/taubyte/tau/libdream/helpers/git"
	protocolCommon "github.com/taubyte/tau/protocols/common"
	"github.com/taubyte/tau/protocols/monkey"

	_ "github.com/taubyte/tau/clients/p2p/monkey"
	_ "github.com/taubyte/tau/clients/p2p/tns"
	_ "github.com/taubyte/tau/protocols/auth"
	_ "github.com/taubyte/tau/protocols/hoarder"
	_ "github.com/taubyte/tau/protocols/tns"

	"testing"
)

func TestConfigJob(t *testing.T) {
	t.Skip("needs to be redone")
	protocolCommon.LocalPatrick = true
	monkey.NewPatrick = func(ctx context.Context, node peer.Node) (patrick.Client, error) {
		return &starfish{Jobs: make(map[string]*patrick.Job, 0)}, nil
	}

	u := dreamland.NewUniverse(dreamland.UniverseConfig{Name: t.Name()})
	defer u.Stop()

	err := u.StartWithConfig(&dreamland.Config{
		Services: map[string]commonIface.ServiceConfig{
			"monkey":  {},
			"hoarder": {},
			"tns":     {},
			"auth":    {},
		},
		Simples: map[string]dreamland.SimpleConfig{
			"client": {
				Clients: dreamland.SimpleConfigClients{
					TNS:    &commonIface.ClientConfig{},
					Monkey: &commonIface.ClientConfig{},
				}.Conform(),
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
	rc, err := compile.CompilerConfig(projectIface, fakJob.Meta)
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

	err = u.Monkey().Patrick().(*starfish).AddJob(t, u.Monkey().Node(), fakJob)
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
