package tests

import (
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	commonIface "github.com/taubyte/tau/core/common"
	iface "github.com/taubyte/tau/core/services/seer"
	"github.com/taubyte/tau/dream"
	"gotest.tools/v3/assert"
)

var client_count = 16

func TestHeartbeat(t *testing.T) {
	u := dream.New(dream.UniverseConfig{Name: t.Name()})
	defer u.Stop()

	simConf := make(map[string]dream.SimpleConfig)
	for i := 0; i < client_count; i++ {
		simConf[fmt.Sprintf("client%d", i)] = dream.SimpleConfig{
			Clients: dream.SimpleConfigClients{
				Seer: &commonIface.ClientConfig{},
				TNS:  &commonIface.ClientConfig{},
			}.Compat(),
		}
	}

	err := u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"seer": {Others: map[string]int{"mock": 1}},
		},
		Simples: simConf,
	})
	if err != nil {
		t.Error(err)
		return
	}

	time.Sleep(10 * time.Second)

	simples := make([]*dream.Simple, len(simConf))

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
