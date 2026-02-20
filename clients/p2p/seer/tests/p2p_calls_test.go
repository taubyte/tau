//go:build dreaming

package tests

import (
	"fmt"
	"testing"
	"time"

	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	"gotest.tools/v3/assert"

	_ "github.com/taubyte/tau/services/auth/dream"
	_ "github.com/taubyte/tau/services/hoarder/dream"
	_ "github.com/taubyte/tau/services/monkey/dream"
	_ "github.com/taubyte/tau/services/patrick/dream"
	_ "github.com/taubyte/tau/services/substrate/dream"
	_ "github.com/taubyte/tau/services/tns/dream"

	_ "github.com/taubyte/tau/clients/p2p/tns/dream"

	seerClient "github.com/taubyte/tau/clients/p2p/seer"
)

func TestCalls_Dreaming(t *testing.T) {
	seerClient.DefaultUsageBeaconInterval = 100 * time.Millisecond
	seerClient.DefaultAnnounceBeaconInterval = 100 * time.Millisecond
	seerClient.DefaultGeoBeaconInterval = 100 * time.Millisecond

	m, err := dream.New(t.Context())
	assert.NilError(t, err)
	defer m.Close()

	u, err := m.New(dream.UniverseConfig{Name: t.Name()})
	assert.NilError(t, err)

	err = u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"seer": {Others: map[string]int{"mock": 1}},
			"auth": {Others: map[string]int{"copies": 2}},
		},
		Simples: map[string]dream.SimpleConfig{
			"client": {
				Clients: dream.SimpleConfigClients{
					Seer: &commonIface.ClientConfig{},
					TNS:  &commonIface.ClientConfig{},
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

	time.Sleep(10 * time.Second)

	seerClient, err := simple.Seer()
	if err != nil {
		t.Error(err)
		return
	}

	serviceIds, err := seerClient.Usage().ListServiceId("auth")
	if err != nil {
		t.Error(err)
		return
	}

	fmt.Println("serviceIds: ", serviceIds)

	if len(serviceIds) != 2 {
		t.Errorf("Expected 2 nodes got %d", len(serviceIds))
	}

}
