package monkey

import (
	"context"
	"testing"

	_ "github.com/taubyte/tau/clients/p2p/monkey"
	"github.com/taubyte/tau/clients/p2p/patrick/mock"
	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/core/services/patrick"
	"github.com/taubyte/tau/dream"
	"github.com/taubyte/tau/p2p/peer"
	protocolCommon "github.com/taubyte/tau/services/common"
	_ "github.com/taubyte/tau/services/hoarder"
	"gotest.tools/v3/assert"
)

func init() {
	NewPatrick = func(ctx context.Context, node peer.Node) (patrick.Client, error) {
		return &mock.Starfish{Jobs: make(map[string]*patrick.Job, 0)}, nil
	}
}

func TestService(t *testing.T) {
	t.Skip("Times out, needs to be relooked at")
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
	if err = u.Monkey().Patrick().(*mock.Starfish).AddJob(t, u.Monkey().Node(), successful_job); err != nil {
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
	if err = u.Monkey().Patrick().(*mock.Starfish).AddJob(t, u.Monkey().Node(), failed_job); err != nil {
		t.Error(err)
		return
	}

	// Test successful job
	monkey, err := simple.Monkey()
	assert.NilError(t, err)

	if err = (&MonkeyTestContext{
		universe:     u,
		client:       monkey,
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
		client:       monkey,
		jid:          failed_job.Id,
		expectStatus: patrick.JobStatusFailed,
		expectLog:    "Running job `fake_jid_failed` was successful",
	}).waitForStatus(); err != nil {
		t.Error(err)
		return
	}

	ids, err := monkey.List()
	if err != nil {
		t.Error(err)
		return
	}

	if len(ids) != 2 {
		t.Errorf("Expected two job ids got %d", len(ids))
		return
	}
}
