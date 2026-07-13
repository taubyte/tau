//go:build dreaming

package tests

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	peercore "github.com/libp2p/go-libp2p/core/peer"
	commonIface "github.com/taubyte/tau/core/common"
	iface "github.com/taubyte/tau/core/services/seer"
	"github.com/taubyte/tau/dream"
	"gotest.tools/v3/assert"

	_ "github.com/taubyte/tau/clients/p2p/seer/dream"
	_ "github.com/taubyte/tau/clients/p2p/tns/dream"
	_ "github.com/taubyte/tau/services/seer/dream"
)

var client_count = 16

func TestHeartbeat_Dreaming(t *testing.T) {
	m, err := dream.New(t.Context())
	assert.NilError(t, err)
	defer m.Close()

	u, err := m.New(dream.UniverseConfig{Name: t.Name()})
	assert.NilError(t, err)

	simConf := make(map[string]dream.SimpleConfig)
	for i := 0; i < client_count; i++ {
		simConf[fmt.Sprintf("client%d", i)] = dream.SimpleConfig{
			Clients: dream.SimpleConfigClients{
				Seer: &commonIface.ClientConfig{},
				TNS:  &commonIface.ClientConfig{},
			}.Compat(),
		}
	}

	err = u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"seer": {Others: map[string]int{"mock": 1}},
		},
		Simples: simConf,
	})
	if err != nil {
		t.Error(err)
		return
	}

	simples := make([]*dream.Simple, len(simConf))

	for i := 0; i < len(simConf); i++ {
		simples[i], err = u.Simple(fmt.Sprintf("client%d", i))
		if err != nil {
			t.Error(err)
			return
		}
	}

	// every client must hold a real (non-limited) connection to the seer
	// before the concurrent load below: a heartbeat has only the 10s send
	// timeout for discovery plus the command roundtrip. The boot mesh is
	// best-effort — under load a pair's initial dial can fail and end up
	// with a relay-limited connection that streams can't open over, and
	// nothing upgrades it — so actively dial stragglers with the seer's
	// direct addresses instead of waiting.
	seerNode := u.Seer().Node()
	seerInfo := peercore.AddrInfo{ID: seerNode.ID(), Addrs: seerNode.Peer().Addrs()}
	deadline := time.Now().Add(60 * time.Second)
	for {
		var unconnected []string
		for i, s := range simples {
			if state := s.PeerNode().Peer().Network().Connectedness(seerNode.ID()); state != network.Connected {
				unconnected = append(unconnected, fmt.Sprintf("client%d=%s", i, state))
				dialCtx, dialC := context.WithTimeout(u.Context(), 5*time.Second)
				if err := s.PeerNode().Peer().Connect(dialCtx, seerInfo); err != nil {
					t.Logf("client%d dial to seer: %v", i, err)
				}
				dialC()
			}
		}
		if len(unconnected) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("clients not connected to the seer within 60s: %v", unconnected)
		}
		time.Sleep(500 * time.Millisecond)
	}

	hostname, err := os.Hostname()
	if err != nil {
		t.Error(err)
		return
	}

	iterations := 256
	poolChan := make(chan bool, client_count)
	var heartbeatWG sync.WaitGroup
	heartbeatWG.Add(iterations)
	for i := 0; i < iterations; i++ {

		poolChan <- true
		go func(i int) {

			defer func() {
				<-poolChan
				heartbeatWG.Done()
			}()
			seer, err := simples[i%len(simConf)].Seer()
			assert.NilError(t, err)

			_, err = seer.Usage().Heartbeat(&iface.UsageData{
				Memory: iface.Memory{
					Used:  10,
					Total: 50,
					Free:  40,
				},
				Cpu: iface.Cpu{
					Total:     12322,
					Count:     12422,
					User:      12522,
					Nice:      21122,
					System:    3100,
					Idle:      4100,
					Iowait:    5100,
					Irq:       6100,
					Softirq:   7100,
					Steal:     8100,
					Guest:     9100,
					GuestNice: 10100,
					StatCount: 11100,
				},
			}, hostname, "", "", nil)
			assert.NilError(t, err)

		}(i)
	}
	heartbeatWG.Wait()

}
