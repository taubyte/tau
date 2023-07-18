package tests

import (
	"os"
	"testing"

	commonDreamland "github.com/taubyte/dreamland/core/common"
	dreamland "github.com/taubyte/dreamland/core/services"
	commonIface "github.com/taubyte/go-interfaces/common"
	iface "github.com/taubyte/go-interfaces/services/seer"
	protocolsCommon "github.com/taubyte/odo/protocols/common"
)

func TestBasicUsage(t *testing.T) {
	u := dreamland.Multiverse("basic_usage")
	defer u.Stop()
	err := u.StartWithConfig(&commonDreamland.Config{
		Services: map[string]commonIface.ServiceConfig{
			"seer":    {Others: map[string]int{"dns": protocolsCommon.DefaultDevDnsPort, "mock": 1}},
			"tns":     {},
			"monkey":  {},
			"patrick": {},
			"auth":    {},
			"hoarder": {},
			"node":    {Others: map[string]int{"copies": 2}},
		},
		Simples: map[string]commonDreamland.SimpleConfig{
			"client": {
				Clients: commonDreamland.SimpleConfigClients{
					Seer: &commonIface.ClientConfig{},
					TNS:  &commonIface.ClientConfig{},
				},
			},
			"clientD": {
				Clients: commonDreamland.SimpleConfigClients{
					Seer: &commonIface.ClientConfig{},
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

	simpleD, err := u.Simple("clientD")
	if err != nil {
		t.Error(err)
		return
	}

	hostname, err := os.Hostname()
	if err != nil {
		t.Error(err)
		return
	}

	// Testing Hearbeat and Announce
	/* Client Heartbeat */
	_, err = simple.Seer().Usage().Heartbeat(&iface.UsageData{
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
	if err != nil {
		t.Error(err)
		return
	}

	/* Client Heartbeat */
	_, err = simple.Seer().Usage().Heartbeat(&iface.UsageData{
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
	if err != nil {
		t.Error(err)
		return
	}

	/* ClientD Heartbeat*/
	_, err = simpleD.Seer().Usage().Heartbeat(&iface.UsageData{
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
	if err != nil {
		t.Error(err)
		return
	}
}
