package service

import (
	"context"
	"testing"

	commonDreamland "bitbucket.org/taubyte/dreamland/common"
	dreamland "bitbucket.org/taubyte/dreamland/services"
	commonIface "github.com/taubyte/go-interfaces/common"
	peer "github.com/taubyte/go-interfaces/p2p/peer"
	"github.com/taubyte/go-interfaces/services/patrick"
	_ "github.com/taubyte/odo/clients/p2p/monkey"
	protocolCommon "github.com/taubyte/odo/protocols/common"
	_ "github.com/taubyte/odo/protocols/hoarder/service"
)

func init() {
	NewPatrick = func(ctx context.Context, node peer.Node) (patrick.Client, error) {
		return &starfish{Jobs: make(map[string]*patrick.Job, 0)}, nil
	}
}

func TestService(t *testing.T) {
	protocolCommon.LocalPatrick = true
	u := dreamland.Multiverse("TestService")
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

	// Get simple client
	simple, err := u.Simple("client")
	if err != nil {
		t.Error(err)
		return
	}

	// Create and add successful job
	successful_job := &patrick.Job{
		Id:       "fake_jid_success",
		Logs:     make(map[string]string),
		AssetCid: make(map[string]string),
	}
	successful_job.Meta.Repository.ID = 1
	if err = u.Monkey().Patrick().(*starfish).AddJob(t, u.Monkey().Node(), successful_job); err != nil {
		t.Error(err)
		return
	}

	// Create and add failed job
	failed_job := &patrick.Job{
		Id:       "fake_jid_failed",
		Logs:     make(map[string]string),
		AssetCid: make(map[string]string),
	}
	failed_job.Meta.Repository.ID = 1
	if err = u.Monkey().Patrick().(*starfish).AddJob(t, u.Monkey().Node(), failed_job); err != nil {
		t.Error(err)
		return
	}

	// Test successful job
	if err = (&MonkeyTestContext{
		universe:     u,
		client:       simple.Monkey(),
		jid:          successful_job.Id,
		expectStatus: patrick.JobStatusSuccess,
		expectLog:    "Running job `fake_jid_success` was successful",
	}).waitForStatus(); err != nil {
		t.Error(err)
		return
	}

	// Test failed job
	if err = (&MonkeyTestContext{
		universe:     u,
		client:       simple.Monkey(),
		jid:          failed_job.Id,
		expectStatus: patrick.JobStatusFailed,
		expectLog:    "Running job `fake_jid_failed` was successful",
	}).waitForStatus(); err != nil {
		t.Error(err)
		return
	}

	ids, err := simple.Monkey().List()
	if err != nil {
		t.Error(err)
		return
	}

	if len(ids) != 2 {
		t.Errorf("Expected two job ids got %d", len(ids))
		return
	}
}
