package tests

import (
	"context"
	"testing"
	"time"

	p2p "github.com/taubyte/tau/clients/p2p/monkey"
	"github.com/taubyte/tau/clients/p2p/patrick/mock"
	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/core/services/patrick"
	"github.com/taubyte/tau/dream"
	"github.com/taubyte/tau/p2p/peer"
	protocolCommon "github.com/taubyte/tau/services/common"
	_ "github.com/taubyte/tau/services/hoarder"
	"github.com/taubyte/tau/services/monkey"
	"gotest.tools/v3/assert"
)

func TestMonkeyClient(t *testing.T) {
	monkey.NewPatrick = func(ctx context.Context, node peer.Node) (patrick.Client, error) {
		return &mock.Starfish{Jobs: make(map[string]*patrick.Job, 0)}, nil
	}

	protocolCommon.MockedPatrick = true

	u := dream.New(dream.UniverseConfig{Name: t.Name()})
	defer u.Stop()

	err := u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"monkey":  {},
			"hoarder": {},
		},
		Simples: map[string]dream.SimpleConfig{
			"client": {
				Clients: dream.SimpleConfigClients{
					Monkey: &commonIface.ClientConfig{},
				}.Compat(),
			},
		},
	})
	if err != nil {
		t.Error(err)
		return
	}
	simple, err := u.Simple("client")
	if err != nil {
		t.Error(err)
		return
	}

	fakJob := &patrick.Job{}
	fakJob.Id = "jobforjob_test"
	fakJob.Logs = make(map[string]string)
	fakJob.AssetCid = make(map[string]string)
	fakJob.Meta.Repository.ID = 1
	fakJob.Meta.Repository.SSHURL = ""
	fakJob.Meta.Repository.Provider = "github"

	err = u.Monkey().Patrick().(*mock.Starfish).AddJob(t, u.Monkey().Node(), fakJob)
	if err != nil {
		t.Error(err)
		return
	}

	time.Sleep(8 * time.Second)

	monkey, err := simple.Monkey()
	assert.NilError(t, err)

	client := monkey.(*p2p.Client)

	resp, err := client.Status(fakJob.Id)
	if err != nil {
		t.Error(err)
		return
	}

	if resp.Status != patrick.JobStatusSuccess {
		t.Errorf("Expected a successful job got %s", resp.Status.String())
		return
	}
}
