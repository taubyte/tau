package tests

import (
	"context"
	"testing"
	"time"

	commonIface "github.com/taubyte/go-interfaces/common"
	"github.com/taubyte/go-interfaces/services/patrick"
	"github.com/taubyte/p2p/peer"
	p2p "github.com/taubyte/tau/clients/p2p/monkey"
	commonDreamland "github.com/taubyte/tau/libdream/common"
	dreamland "github.com/taubyte/tau/libdream/services"
	protocolCommon "github.com/taubyte/tau/protocols/common"
	_ "github.com/taubyte/tau/protocols/hoarder"
	"github.com/taubyte/tau/protocols/monkey"
)

func TestMonkeyClient(t *testing.T) {
	monkey.NewPatrick = func(ctx context.Context, node peer.Node) (patrick.Client, error) {
		return &starfish{Jobs: make(map[string]*patrick.Job, 0)}, nil
	}

	protocolCommon.LocalPatrick = true

	u := dreamland.Multiverse(dreamland.UniverseConfig{Name: t.Name()})
	defer u.Stop()

	err := u.StartWithConfig(&commonDreamland.Config{
		Services: map[string]commonIface.ServiceConfig{
			"monkey":  {},
			"hoarder": {},
		},
		Simples: map[string]commonDreamland.SimpleConfig{
			"client": {
				Clients: commonDreamland.SimpleConfigClients{
					Monkey: &commonIface.ClientConfig{},
				},
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

	err = u.Monkey().Patrick().(*starfish).AddJob(t, u.Monkey().Node(), fakJob)
	if err != nil {
		t.Error(err)
		return
	}

	time.Sleep(8 * time.Second)

	client := simple.Monkey().(*p2p.Client)

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
