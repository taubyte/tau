package prompt

import (
	"fmt"
	"sync"

	goPrompt "github.com/c-bata/go-prompt"
	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/libp2p/go-libp2p/core/peer"
)

var swarmTree = &tctree{
	leafs: []*leaf{
		exitLeaf,
		{
			validator: stringValidator("all"),
			ret: []goPrompt.Suggest{
				{
					Text:        "all",
					Description: "show swarm",
				},
			},
			handler: swarmList,
		},
		{
			validator: stringValidator("healthy"),
			ret: []goPrompt.Suggest{
				{
					Text:        "healthy",
					Description: "show swarm with ping status",
				},
			},
			handler: swarmHealth,
		},
	},
}

func swarmList(p Prompt, args []string) error {
	t := table.NewWriter()
	t.AppendHeader(table.Row{"PID", "Address"})
	t.SetStyle(table.StyleLight)
	for _, pid := range prompt.Node().Peer().Peerstore().Peers() {
		peerInfo := prompt.Node().Peer().Peerstore().PeerInfo(pid)
		if len(peerInfo.Addrs) > 0 {
			t.AppendRows([]table.Row{{pid.String(), peerInfo.Addrs[0].String()}},
				table.RowConfig{})
		} else {
			t.AppendRows([]table.Row{{pid.String(), "-"}},
				table.RowConfig{})
		}
		t.AppendSeparator()
	}

	fmt.Println(t.Render())
	return nil
}

func swarmHealth(p Prompt, args []string) error {
	var (
		wg   sync.WaitGroup
		lock sync.Mutex
	)
	t := table.NewWriter()
	t.AppendHeader(table.Row{"PID", "Address", "Count", "time"})
	t.SetStyle(table.StyleLight)
	for _, pid := range prompt.Node().Peer().Peerstore().Peers() {
		wg.Add(1)
		go func(_pid peer.ID) {
			peerInfo := prompt.Node().Peer().Peerstore().PeerInfo(_pid)
			count, time, err := prompt.Node().Ping(_pid.String(), 3)
			pid := _pid.String()
			addr := peerInfo.Addrs[0].String()
			lock.Lock()
			if err != nil {
				t.AppendRows([]table.Row{{pid, addr, "--", "--"}},
					table.RowConfig{})
			} else {
				t.AppendRows([]table.Row{{pid, addr, count, time}},
					table.RowConfig{})
			}
			t.AppendSeparator()
			lock.Unlock()
			wg.Done()
		}(pid)
	}
	wg.Wait()

	fmt.Println(t.Render())
	return nil
}
