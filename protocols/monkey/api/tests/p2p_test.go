package tests

import (
	"context"
	"testing"
	"time"

	commonDreamland "bitbucket.org/taubyte/dreamland/common"
	dreamland "bitbucket.org/taubyte/dreamland/services"
	commonIface "github.com/taubyte/go-interfaces/common"
	peer "github.com/taubyte/go-interfaces/p2p/peer"
	"github.com/taubyte/go-interfaces/services/patrick"
	_ "github.com/taubyte/odo/protocols/hoarder/service"
	"github.com/taubyte/odo/protocols/monkey/api/p2p"
	"github.com/taubyte/odo/protocols/monkey/common"
	"github.com/taubyte/odo/protocols/monkey/service"
)

func TestClient(t *testing.T) {
	service.NewPatrick = func(ctx context.Context, node peer.Node) (patrick.Client, error) {
		return &starfish{Jobs: make(map[string]*patrick.Job, 0)}, nil
	}

	common.LocalPatrick = true

	u := dreamland.Multiverse("TestClient")
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
