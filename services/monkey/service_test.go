//go:build dreaming

package monkey

import (
	"context"
	"testing"

	_ "github.com/taubyte/tau/clients/p2p/monkey/dream"
	"github.com/taubyte/tau/clients/p2p/patrick/mock"
	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/core/services/patrick"
	"github.com/taubyte/tau/dream"
	dreamCommon "github.com/taubyte/tau/dream/common"
	"github.com/taubyte/tau/p2p/peer"
	commonSpecs "github.com/taubyte/tau/pkg/specs/common"
	protocolCommon "github.com/taubyte/tau/services/common"
	_ "github.com/taubyte/tau/services/hoarder/dream"
	"gotest.tools/v3/assert"
)

func init() {
	// This test is inside package monkey, so it cannot import monkey/dream (an
	// import cycle); register the service with Dream the way that package does.
	if err := dream.Registry.Set(commonSpecs.Monkey, func(u *dream.Universe, config *commonIface.ServiceConfig) (commonIface.Service, error) {
		cfg, err := dreamCommon.NewConfig(u, config)
		if err != nil {
			return nil, err
		}
		return New(u.Context(), cfg)
	}, nil); err != nil {
		panic(err)
	}

	NewPatrick = func(ctx context.Context, node peer.Node) (patrick.Client, error) {
		return &mock.Starfish{Jobs: make(map[string]*patrick.Job, 0)}, nil
	}
}

func TestService_Dreaming(t *testing.T) {
	protocolCommon.MockedPatrick = true
	m, err := dream.New(t.Context())
	assert.NilError(t, err)
	defer m.Close()

	u, err := m.New(dream.UniverseConfig{Name: t.Name()})
	assert.NilError(t, err)

	err = u.StartWithConfig(&dream.Config{
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
	// Repository ID 0 is RunJob's defined failure: no repository.
	failed_job.Meta.Repository.ID = 0
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
		expectLog:    "Running job `fake_jid_failed` failed with error: no repository defined for job: fake_jid_failed",
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
