package tests

import (
	"os"
	"testing"

	commonIface "github.com/taubyte/tau/core/common"
	iface "github.com/taubyte/tau/core/services/seer"
	"github.com/taubyte/tau/dream"
	_ "github.com/taubyte/tau/services/gateway"
	"gotest.tools/v3/assert"
)

func TestBasicUsage(t *testing.T) {
	u := dream.New(dream.UniverseConfig{Name: t.Name()})
	defer u.Stop()
	err := u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"seer":      {Others: map[string]int{"mock": 1}},
			"tns":       {},
			"monkey":    {},
			"patrick":   {},
			"auth":      {},
			"hoarder":   {},
			"substrate": {Others: map[string]int{"copies": 2}},
			"gateway":   {},
		},
		Simples: map[string]dream.SimpleConfig{
			"client": {
				Clients: dream.SimpleConfigClients{
					Seer: &commonIface.ClientConfig{},
					TNS:  &commonIface.ClientConfig{},
				}.Compat(),
			},
			"clientD": {
				Clients: dream.SimpleConfigClients{
					Seer: &commonIface.ClientConfig{},
				}.Compat(),
			},
		},
	})
	assert.NilError(t, err)

	simple, err := u.Simple("client")
	assert.NilError(t, err)

	simpleD, err := u.Simple("clientD")
	assert.NilError(t, err)

	hostname, err := os.Hostname()
	assert.NilError(t, err)

	// Testing Hearbeat and Announce
	/* Client Heartbeat */

	seer, err := simple.Seer()
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

	/* Client Heartbeat */
	_, err = seer.Usage().Heartbeat(&iface.UsageData{
		Memory: iface.Memory{
			Used:  20,
			Total: 100,
			Free:  80,
		},
		Cpu: iface.Cpu{
			Total:     123,
			Count:     124,
			User:      125,
			Nice:      211,
			System:    31,
			Idle:      41,
			Iowait:    51,
			Irq:       61,
			Softirq:   71,
			Steal:     81,
			Guest:     91,
			GuestNice: 101,
			StatCount: 111,
		},
	}, hostname, "", "", nil)
	assert.NilError(t, err)

	/* ClientD Heartbeat*/
	dSeer, err := simpleD.Seer()
	assert.NilError(t, err)

	_, err = dSeer.Usage().Heartbeat(&iface.UsageData{
		Memory: iface.Memory{
			Used:  40,
			Total: 200,
			Free:  160,
		},
		Cpu: iface.Cpu{
			Total:     444,
			Count:     876,
			User:      1,
			Nice:      2,
			System:    3,
			Idle:      4,
			Iowait:    5,
			Irq:       6,
			Softirq:   7,
			Steal:     8,
			Guest:     9,
			GuestNice: 10,
			StatCount: 11,
		},
	}, hostname, "", "", nil)
	assert.NilError(t, err)
}
