package tests

import (
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	commonIface "github.com/taubyte/go-interfaces/common"
	iface "github.com/taubyte/go-interfaces/services/seer"
	dreamland "github.com/taubyte/tau/libdream"
	"gotest.tools/v3/assert"
)

var client_count = 16

func TestHeartbeat(t *testing.T) {
	u := dreamland.New(dreamland.UniverseConfig{Name: t.Name()})
	defer u.Stop()

	simConf := make(map[string]dreamland.SimpleConfig)
	for i := 0; i < client_count; i++ {
		simConf[fmt.Sprintf("client%d", i)] = dreamland.SimpleConfig{
			Clients: dreamland.SimpleConfigClients{
				Seer: &commonIface.ClientConfig{},
				TNS:  &commonIface.ClientConfig{},
			}.Compat(),
		}
	}

	err := u.StartWithConfig(&dreamland.Config{
		Services: map[string]commonIface.ServiceConfig{
			"seer": {Others: map[string]int{"mock": 1}},
		},
		Simples: simConf,
	})
	if err != nil {
		t.Error(err)
		return
	}

	time.Sleep(2 * time.Second)

	simples := make([]*dreamland.Simple, len(simConf))

	for i := 0; i < len(simConf); i++ {
		simples[i], err = u.Simple(fmt.Sprintf("client%d", i))
		if err != nil {
			t.Error(err)
			return
		}
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
