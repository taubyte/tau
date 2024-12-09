package prompt

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/jedib0t/go-pretty/v6/table"
)

func pingCMD(p Prompt, args []string) error {
	if len(args) < 2 {
		return errors.New("must provide PID")
	}
	pid := args[1]
	_pid, err := peer.Decode(pid)
	if err != nil {
		return fmt.Errorf("peer id `%s` is invalid", pid)
	}

	count, time, err := prompt.Node().Ping(context.TODO(), pid, 3)

	t := table.NewWriter()
	t.SetStyle(table.StyleLight)
	t.SetOutputMirror(os.Stdout)
	t.SetColumnConfigs([]table.ColumnConfig{
		{
			Number:    1,
			AutoMerge: true,
		},
	})

	t.AppendRows([]table.Row{
		{"Host", pid, pid},
	}, table.RowConfig{AutoMerge: true})
	t.AppendSeparator()

	_addrs := prompt.Node().Peer().Peerstore().PeerInfo(_pid).Addrs
	_row := make([]table.Row, len(_addrs))
	for i, _addr := range _addrs {
		_row[i] = table.Row{"Addresses", _addr.String(), _addr.String()}
	}
	t.AppendRows(_row, table.RowConfig{AutoMerge: true})

	t.AppendSeparator()
	if err != nil {
		t.AppendRows([]table.Row{
			{"Error", err},
		})
	} else {
		t.AppendRows([]table.Row{
			{"Stats", "Count", count},
			{"Stats", "Time", time},
		}, table.RowConfig{AutoMerge: true})
	}

	t.Render()

	return nil
}
